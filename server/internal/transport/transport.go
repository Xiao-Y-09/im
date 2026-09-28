package transport

import (
	"context"
	"errors"

	"im/internal/model"
)

var (
	ErrBusy   = errors.New("transport busy")
	ErrClosed = errors.New("transport closed")
)

type Handler func(env *model.Envelope)

type Subscription struct {
	Name      string // "deliver" | "persist"
	Broadcast bool   // true: 每个实例各收一份；false: 所有实例合起来只收一份
}

type Transport interface {
	Publish(ctx context.Context, env *model.Envelope) error
	Subscribe(ctx context.Context, sub Subscription, h Handler) error // 必须在 Start 前调用
	Start(ctx context.Context) error
	Close() error
}
