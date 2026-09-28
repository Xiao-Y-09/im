package service

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const MaxUploadBytes = 20 << 20

var safeExtRe = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

type FileService struct {
	dir string
}

// NewFileService 在启动时就建好目录，避免第一次上传才发现没有权限。
func NewFileService(dir string) (*FileService, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileService{dir: dir}, nil
}

type FileView struct {
	URL  string `json:"url"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
}

// Save 用 uuid 做存储名：原文件名只用来展示，不参与路径，杜绝路径穿越和同名覆盖。
func (s *FileService) Save(r io.Reader, origName string) (FileView, error) {
	ext := strings.ToLower(filepath.Ext(origName))
	if !safeExtRe.MatchString(ext) {
		ext = ""
	}
	stored := uuid.NewString() + ext
	path := filepath.Join(s.dir, stored)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return FileView{}, internalErr(err)
	}

	// 只看前 512 字节判断类型，不信任客户端的 Content-Type
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		f.Close()
		os.Remove(path)
		return FileView{}, internalErr(err)
	}
	head = head[:n]
	mime := http.DetectContentType(head)

	written, err := f.Write(head)
	var rest int64
	if err == nil {
		rest, err = io.Copy(f, io.LimitReader(r, MaxUploadBytes+1-int64(written)))
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	size := int64(written) + rest
	if err == nil && size > MaxUploadBytes {
		err = newErr(CodeTooLarge, "file exceeds 20MB")
	}
	if err != nil {
		os.Remove(path)
		var se *Error
		if errors.As(err, &se) {
			return FileView{}, se
		}
		return FileView{}, internalErr(err)
	}

	name := filepath.Base(strings.ReplaceAll(origName, `\`, "/"))
	if name == "." || name == "/" || name == "" {
		name = stored
	}
	return FileView{URL: "/files/" + stored, Name: name, Size: size, Mime: mime}, nil
}
