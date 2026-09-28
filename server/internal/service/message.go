package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"im/internal/dao"
	"im/internal/metrics"
	"im/internal/model"
	"im/internal/transport"
)

const (
	maxPayloadBytes   = 16 << 10
	maxClientMsgIDLen = 36 // 对应 messages.client_msg_id CHAR(36)
	defaultHistory    = 50
	maxHistory        = 200
	publishTimeout    = 5 * time.Second
)

// OnlineChecker 由 presence 包实现，用来在呼叫前判断被叫是否在线。
type OnlineChecker interface {
	IsOnline(ctx context.Context, uid int64) (bool, error)
}

type MessageService struct {
	msgs      *dao.MessageDAO
	conv      *ConvService
	transport transport.Transport
	presence  OnlineChecker
}

func NewMessageService(msgs *dao.MessageDAO, conv *ConvService, t transport.Transport, p OnlineChecker) *MessageService {
	return &MessageService{msgs: msgs, conv: conv, transport: t, presence: p}
}

func (s *MessageService) History(uid int64, convID string, afterSeq int64, limit int) ([]model.Envelope, error) {
	if err := s.conv.CanAccess(uid, convID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultHistory
	}
	if limit > maxHistory {
		limit = maxHistory
	}
	if afterSeq < 0 {
		afterSeq = 0
	}
	ms, err := s.msgs.ListAfter(convID, afterSeq, limit)
	if err != nil {
		return nil, internalErr(err)
	}
	out := make([]model.Envelope, 0, len(ms))
	for _, m := range ms {
		out = append(out, model.Envelope{
			Type:        m.Type,
			ConvID:      m.ConvID,
			From:        m.SenderID,
			ClientMsgID: m.ClientMsgID,
			Seq:         m.Seq,
			Payload:     json.RawMessage(m.Content),
			TS:          m.CreatedAt.UnixMilli(),
		})
	}
	return out, nil
}

// Send 校验并发布一条客户端消息。返回值非 nil 时，调用方要把它只回给发出这条消息的连接
// （错误，或者被叫离线时的 call_reject）；成功发布时返回 nil，发送者靠回显确认。
func (s *MessageService) Send(ctx context.Context, uid int64, env *model.Envelope) *model.Envelope {
	// 这几个字段一律由服务端决定，客户端传什么都覆盖掉
	env.From = uid
	env.TS = time.Now().UnixMilli()
	env.Seq = 0

	if err := s.validate(uid, env); err != nil {
		return errorReply(env.ClientMsgID, err)
	}

	if env.Type == model.TypeCallInvite {
		online, err := s.presence.IsOnline(ctx, env.To)
		if err != nil {
			log.Printf("presence check uid=%d: %v", env.To, err)
			return errorReply(env.ClientMsgID, newErr(CodeUnavailable, "presence unavailable"))
		}
		if !online {
			return offlineReject(env)
		}
	}

	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := s.transport.Publish(pctx, env); err != nil {
		if errors.Is(err, transport.ErrBusy) {
			metrics.BusyRejected.Add(1)
			return errorReply(env.ClientMsgID, newErr(CodeBusy, "server busy, retry later"))
		}
		log.Printf("publish from uid=%d: %v", uid, err)
		return errorReply(env.ClientMsgID, newErr(CodeUnavailable, "message transport unavailable"))
	}
	metrics.Published.Add(1)
	return nil
}

func (s *MessageService) validate(uid int64, env *model.Envelope) error {
	if !model.IsSendable(env.Type) {
		return newErr(CodeBadRequest, "unknown message type %q", env.Type)
	}
	if env.ClientMsgID == "" || len(env.ClientMsgID) > maxClientMsgIDLen {
		return newErr(CodeBadRequest, "client_msg_id is required (max 36 chars)")
	}
	if len(env.Payload) > maxPayloadBytes {
		return newErr(CodeTooLarge, "payload exceeds 16KB")
	}
	if len(env.Payload) == 0 || string(env.Payload) == "null" {
		env.Payload = json.RawMessage("{}")
	}

	if model.IsCallType(env.Type) {
		if env.To == 0 || env.To == uid {
			return newErr(CodeBadRequest, "call signal needs a valid 'to'")
		}
		// 通话信令的 conv_id 由服务端推出，保证 Kafka key 稳定
		env.ConvID = model.PrivateConvID(uid, env.To)
		return nil
	}

	env.To = 0
	if err := s.conv.CanAccess(uid, env.ConvID); err != nil {
		return err
	}
	if env.Type == model.TypeFile {
		return validateFilePayload(env.Payload)
	}
	return nil
}

type filePayload struct {
	URL  *string `json:"url"`
	Name *string `json:"name"`
	Size *int64  `json:"size"`
	Mime *string `json:"mime"`
}

// validateFilePayload 只允许指向本服务 /files/ 的链接，防止借消息传播外部恶意地址。
func validateFilePayload(raw json.RawMessage) error {
	var p filePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return newErr(CodeBadRequest, "file payload must be an object")
	}
	if p.URL == nil || p.Name == nil || p.Size == nil || p.Mime == nil {
		return newErr(CodeBadRequest, "file payload requires url, name, size, mime")
	}
	u := *p.URL
	if !strings.HasPrefix(u, "/files/") || strings.Contains(u, "..") || strings.Contains(u, "//") {
		return newErr(CodeBadRequest, "file url must start with /files/")
	}
	if *p.Size < 0 {
		return newErr(CodeBadRequest, "file size must be >= 0")
	}
	return nil
}

func errorReply(clientMsgID string, err error) *model.Envelope {
	var se *Error
	if errors.As(err, &se) {
		return model.ErrorEnvelope(clientMsgID, se.Code, se.Msg)
	}
	return model.ErrorEnvelope(clientMsgID, CodeInternal, err.Error())
}

// offlineReject 伪装成被叫发出的拒绝，前端就能用同一套逻辑处理“对方拒绝”和“对方不在线”。
func offlineReject(env *model.Envelope) *model.Envelope {
	p, _ := json.Marshal(map[string]string{"reason": "offline"})
	return &model.Envelope{
		Type:        model.TypeCallReject,
		ConvID:      env.ConvID,
		From:        env.To,
		To:          env.From,
		ClientMsgID: env.ClientMsgID,
		Payload:     p,
		TS:          time.Now().UnixMilli(),
	}
}
