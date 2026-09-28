package ws

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"

	"im/internal/model"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 30 * time.Second
	maxMessageSize = 64 << 10
	sendBufferSize = 256
)

// MessageHandler 由 api 层注入（最终调用 MessageService.Send），ws 包因此不依赖业务层。
type MessageHandler func(c *Client, env *model.Envelope)

// Client 是一个 WS 连接。gorilla/websocket 不允许并发读或并发写，
// 所以固定 readPump 是唯一的读者、writePump 是唯一的写者，其他人只能往 send 里放数据。
type Client struct {
	UserID    int64
	conn      *websocket.Conn
	send      chan []byte
	hub       *Hub
	onMessage MessageHandler
}

func NewClient(hub *Hub, conn *websocket.Conn, uid int64, onMessage MessageHandler) *Client {
	return &Client{
		UserID:    uid,
		conn:      conn,
		send:      make(chan []byte, sendBufferSize),
		hub:       hub,
		onMessage: onMessage,
	}
}

// Reply 只发给这一个连接（不经过 Transport），用于错误等只和发送者有关的回复。
func (c *Client) Reply(env *model.Envelope) {
	b, err := json.Marshal(env)
	if err != nil {
		log.Printf("reply marshal uid=%d: %v", c.UserID, err)
		return
	}
	c.hub.SendTo(c, b)
}

func (c *Client) ReadPump() {
	defer c.hub.Unregister(c)

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				log.Printf("ws read uid=%d: %v", c.UserID, err)
			}
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))

		var env model.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			c.Reply(model.ErrorEnvelope("", "bad_request", "invalid JSON envelope"))
			continue
		}
		c.onMessage(c, &env)
	}
}

func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close() // 让 readPump 的 ReadMessage 出错退出
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub 已经注销了这个连接（主动断开或被当作慢客户端踢掉）
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
			c.hub.refreshPresence(c.UserID)
		}
	}
}
