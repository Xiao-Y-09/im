package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"im/internal/service"
)

// multipart 边界和表单头的开销；文件本体的 20MB 上限由 FileService 精确判断。
const multipartOverhead = 64 << 10

func (a *API) upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxUploadBytes+multipartOverhead)

	fh, err := c.FormFile("file")
	if err != nil {
		if isTooLarge(err) {
			abortErr(c, http.StatusRequestEntityTooLarge, service.CodeTooLarge, "file exceeds 20MB")
			return
		}
		badRequest(c, "multipart field 'file' is required")
		return
	}
	if fh.Size > service.MaxUploadBytes {
		abortErr(c, http.StatusRequestEntityTooLarge, service.CodeTooLarge, "file exceeds 20MB")
		return
	}
	f, err := fh.Open()
	if err != nil {
		writeErr(c, err)
		return
	}
	defer f.Close()

	v, err := a.files.Save(f, fh.Filename)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func isTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe) || strings.Contains(err.Error(), "request body too large")
}

// fileHeaders 防止上传的 HTML/SVG 在本站同源下被当成页面执行。
func fileHeaders(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox")
	c.Next()
}
