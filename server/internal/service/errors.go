package service

import "fmt"

// 错误码同时用于 HTTP（api 层映射成状态码）和 WS（直接放进 error payload）。
const (
	CodeBadRequest   = "bad_request"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeTooLarge     = "too_large"
	CodeBusy         = "busy"
	CodeUnavailable  = "unavailable"
	CodeInternal     = "internal"
)

type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func newErr(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// internalErr 把底层错误包成 internal；原始错误由调用方 log，不回给客户端。
func internalErr(err error) *Error {
	return &Error{Code: CodeInternal, Msg: "internal error: " + err.Error()}
}
