package local

import (
	"context"
	"errors"
	"sync"
	"time"

	"im/internal/config"
	"im/internal/model"
	"im/internal/transport"
)

// Local 用一个有界 channel 做单机消息总线。
// 只有一个 dispatcher goroutine 出队，所以 seq 在它手里自增就天然全局有序、无需加锁。
type Local struct {
	ch      chan *model.Envelope
	timeout time.Duration
	seq     int64 // 只被 dispatcher 读写

	timers sync.Pool

	mu       sync.Mutex
	handlers []transport.Handler
	started  bool
	closed   bool

	quit chan struct{}
	done chan struct{}
}

var _ transport.Transport = (*Local)(nil)

func New(cfg config.LocalConfig, initialSeq int64) *Local {
	return &Local{
		ch:      make(chan *model.Envelope, cfg.BufferSize),
		timeout: time.Duration(cfg.PublishTimeoutMS) * time.Millisecond,
		seq:     initialSeq,
		quit:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Publish 在队列满时最多等 timeout，然后返回 ErrBusy。这就是背压：
// 慢的是下游，就让上游（发送者）立刻知道，而不是无限堆积在内存里。
func (l *Local) Publish(ctx context.Context, env *model.Envelope) error {
	select {
	case <-l.quit:
		return transport.ErrClosed
	default:
	}

	// 快路径：有空位就不碰定时器
	select {
	case l.ch <- env:
		return nil
	default:
	}

	t := l.getTimer()
	defer l.putTimer(t)
	select {
	case l.ch <- env:
		return nil
	case <-t.C:
		return transport.ErrBusy
	case <-ctx.Done():
		return ctx.Err()
	case <-l.quit:
		return transport.ErrClosed
	}
}

// 复用 Timer：time.After 在高频调用下每次都分配一个定时器，超时前不会被回收。
func (l *Local) getTimer() *time.Timer {
	if v := l.timers.Get(); v != nil {
		t := v.(*time.Timer)
		t.Reset(l.timeout)
		return t
	}
	return time.NewTimer(l.timeout)
}

func (l *Local) putTimer(t *time.Timer) {
	if !t.Stop() {
		// 已触发但还没被读走的值要清掉，否则下次 Reset 后会立刻误判超时
		select {
		case <-t.C:
		default:
		}
	}
	l.timers.Put(t)
}

// Subscribe 在单机模式下不区分 Broadcast：只有一个实例，两种语义等价。
func (l *Local) Subscribe(_ context.Context, _ transport.Subscription, h transport.Handler) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return errors.New("local transport: Subscribe after Start")
	}
	l.handlers = append(l.handlers, h)
	return nil
}

func (l *Local) Start(_ context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return errors.New("local transport: already started")
	}
	l.started = true
	handlers := append([]transport.Handler(nil), l.handlers...)
	go l.dispatch(handlers)
	return nil
}

func (l *Local) dispatch(handlers []transport.Handler) {
	defer close(l.done)
	for {
		select {
		case env := <-l.ch:
			l.handle(handlers, env)
		case <-l.quit:
			// 关闭前把已经接收的消息处理完，它们的发送者已经被告知成功了
			for {
				select {
				case env := <-l.ch:
					l.handle(handlers, env)
				default:
					return
				}
			}
		}
	}
}

func (l *Local) handle(handlers []transport.Handler, env *model.Envelope) {
	l.seq++
	env.Seq = l.seq
	for _, h := range handlers {
		h(env)
	}
}

func (l *Local) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	started := l.started
	close(l.quit)
	l.mu.Unlock()

	if started {
		<-l.done
	}
	return nil
}
