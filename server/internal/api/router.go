package api

import (
	"context"
	"expvar"
	"net/http"
	"runtime"

	"github.com/gin-gonic/gin"

	"im/internal/service"
	"im/internal/ws"
)

type Deps struct {
	BaseCtx   context.Context // WS 消息处理用；进程退出时取消
	UploadDir string
	Users     *service.UserService
	Groups    *service.GroupService
	Messages  *service.MessageService
	Files     *service.FileService
	Presence  *service.PresenceService
	Hub       *ws.Hub
}

type API struct {
	baseCtx  context.Context
	users    *service.UserService
	groups   *service.GroupService
	messages *service.MessageService
	files    *service.FileService
	presence *service.PresenceService
	hub      *ws.Hub
}

func NewRouter(d Deps) *gin.Engine {
	a := &API{
		baseCtx:  d.BaseCtx,
		users:    d.Users,
		groups:   d.Groups,
		messages: d.Messages,
		files:    d.Files,
		presence: d.Presence,
		hub:      d.Hub,
	}

	r := gin.New()
	r.Use(gin.Recovery())
	// 比默认 32MB 小：超过的部分落临时文件，避免大文件并发上传吃内存
	r.MaxMultipartMemory = 8 << 20

	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	r.GET("/debug/vars", gin.WrapH(expvar.Handler()))
	r.GET("/debug/goroutines", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"n": runtime.NumGoroutine()})
	})

	r.Group("/files", fileHeaders).Static("/", d.UploadDir)

	auth := JWTAuth(d.Users)
	r.GET("/ws", auth, a.serveWS)

	pub := r.Group("/api")
	pub.POST("/register", a.register)
	pub.POST("/login", a.login)

	priv := r.Group("/api", auth)
	priv.GET("/me", a.me)
	priv.GET("/users", a.searchUsers)
	priv.POST("/groups", a.createGroup)
	priv.POST("/groups/:id/join", a.joinGroup)
	priv.GET("/groups/:id/members", a.groupMembers)
	priv.GET("/my/groups", a.myGroups)
	priv.GET("/messages", a.history)
	priv.GET("/presence", a.presenceQuery)
	priv.POST("/upload", a.upload)

	return r
}
