package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"im/internal/service"
)

const ctxUID = "uid"

// JWTAuth 同时接受 header 和 query token：浏览器的 WebSocket API 没法设置自定义 header。
func JWTAuth(users *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := ""
		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tok = strings.TrimPrefix(h, "Bearer ")
		} else {
			tok = c.Query("token")
		}
		if tok == "" {
			abortErr(c, http.StatusUnauthorized, service.CodeUnauthorized, "missing token")
			return
		}
		uid, err := users.ParseToken(tok)
		if err != nil {
			writeErr(c, err)
			return
		}
		c.Set(ctxUID, uid)
		c.Next()
	}
}

func uidOf(c *gin.Context) int64 {
	return c.GetInt64(ctxUID)
}
