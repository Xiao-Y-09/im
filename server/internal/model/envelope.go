package model

import (
	"encoding/json"
	"strings"
)

const (
	TypeChat       = "chat"
	TypeFile       = "file"
	TypeCallInvite = "call_invite"
	TypeCallAccept = "call_accept"
	TypeCallReject = "call_reject"
	TypeCallOffer  = "call_offer"
	TypeCallAnswer = "call_answer"
	TypeCallICE    = "call_ice"
	TypeCallHangup = "call_hangup"
	TypeError      = "error"
)

type Envelope struct {
	Type        string          `json:"type"`
	ConvID      string          `json:"conv_id"`
	From        int64           `json:"from"`
	To          int64           `json:"to,omitempty"`
	ClientMsgID string          `json:"client_msg_id"`
	Seq         int64           `json:"seq"`
	Payload     json.RawMessage `json:"payload"`
	TS          int64           `json:"ts"`
}

func IsCallType(t string) bool {
	return strings.HasPrefix(t, "call_")
}

// IsSendable 是客户端允许发送的 type 白名单；error 只能由服务端产生。
func IsSendable(t string) bool {
	switch t {
	case TypeChat, TypeFile,
		TypeCallInvite, TypeCallAccept, TypeCallReject,
		TypeCallOffer, TypeCallAnswer, TypeCallICE, TypeCallHangup:
		return true
	}
	return false
}

// ErrorEnvelope 构造只回给发送者的错误消息。
func ErrorEnvelope(clientMsgID, code, message string) *Envelope {
	p, _ := json.Marshal(map[string]string{"code": code, "message": message})
	return &Envelope{Type: TypeError, ClientMsgID: clientMsgID, Payload: p}
}
