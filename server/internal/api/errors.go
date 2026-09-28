package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"im/internal/service"
)

var statusByCode = map[string]int{
	service.CodeBadRequest:   http.StatusBadRequest,
	service.CodeUnauthorized: http.StatusUnauthorized,
	service.CodeForbidden:    http.StatusForbidden,
	service.CodeNotFound:     http.StatusNotFound,
	service.CodeConflict:     http.StatusConflict,
	service.CodeTooLarge:     http.StatusRequestEntityTooLarge,
	service.CodeBusy:         http.StatusServiceUnavailable,
	service.CodeUnavailable:  http.StatusServiceUnavailable,
	service.CodeInternal:     http.StatusInternalServerError,
}

func abortErr(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": code, "message": msg})
}

// writeErr 把 service 错误翻译成统一的 {"error","message"}；内部错误只 log 细节，不外泄。
func writeErr(c *gin.Context, err error) {
	var se *service.Error
	if !errors.As(err, &se) {
		log.Printf("%s %s: %v", c.Request.Method, c.Request.URL.Path, err)
		abortErr(c, http.StatusInternalServerError, service.CodeInternal, "internal error")
		return
	}
	status, ok := statusByCode[se.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	if status == http.StatusInternalServerError {
		log.Printf("%s %s: %v", c.Request.Method, c.Request.URL.Path, se)
		abortErr(c, status, se.Code, "internal error")
		return
	}
	abortErr(c, status, se.Code, se.Msg)
}

func badRequest(c *gin.Context, msg string) {
	abortErr(c, http.StatusBadRequest, service.CodeBadRequest, msg)
}
