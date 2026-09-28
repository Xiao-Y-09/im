package api

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"im/internal/model"
	"im/internal/ws"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// TODO: 生产环境改成只允许前端域名；开发阶段 vite 代理和脚本的 Origin 各不相同，先放行
	CheckOrigin: func(*http.Request) bool { return true },
}

func (a *API) serveWS(c *gin.Context) {
	uid := uidOf(c)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ws upgrade uid=%d: %v", uid, err) // Upgrade 已经写好了 HTTP 错误响应
		return
	}
	client := ws.NewClient(a.hub, conn, uid, a.onWSMessage)
	a.hub.Register(client)
	go client.WritePump()
	go client.ReadPump()
}

func (a *API) onWSMessage(c *ws.Client, env *model.Envelope) {
	if reply := a.messages.Send(a.baseCtx, c.UserID, env); reply != nil {
		c.Reply(reply)
	}
}
