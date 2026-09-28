package local

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"im/internal/config"
	"im/internal/model"
	"im/internal/transport"
)

func TestConcurrentPublishSeqContiguous(t *testing.T) {
	const (
		publishers = 100
		perPub     = 100
		total      = publishers * perPub
		initial    = 41
	)
	l := New(config.LocalConfig{BufferSize: 256, PublishTimeoutMS: 5000}, initial)

	var mu sync.Mutex
	seqs := make([]int64, 0, total)
	got := make(chan struct{})
	h := func(env *model.Envelope) {
		mu.Lock()
		seqs = append(seqs, env.Seq)
		if len(seqs) == total {
			close(got)
		}
		mu.Unlock()
	}
	ctx := context.Background()
	if err := l.Subscribe(ctx, transport.Subscription{Name: "test", Broadcast: true}, h); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	var wg sync.WaitGroup
	errs := make(chan error, publishers)
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perPub; i++ {
				if err := l.Publish(ctx, &model.Envelope{Type: model.TypeChat}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatalf("handler got %d/%d messages", len(seqs), total)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, s := range seqs {
		if want := int64(initial + i + 1); s != want {
			t.Fatalf("seqs[%d] = %d, want %d", i, s, want)
		}
	}
}

func TestPublishBusyWhenFull(t *testing.T) {
	l := New(config.LocalConfig{BufferSize: 1, PublishTimeoutMS: 1}, 0)
	release := make(chan struct{})
	h := func(*model.Envelope) { <-release }
	ctx := context.Background()
	if err := l.Subscribe(ctx, transport.Subscription{Name: "slow"}, h); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(release)
		l.Close()
	}()

	// 第 1 条被 dispatcher 取走卡在 handler 里，第 2 条占满 buffer，之后必然 busy
	var busy bool
	for i := 0; i < 10; i++ {
		err := l.Publish(ctx, &model.Envelope{Type: model.TypeChat})
		if errors.Is(err, transport.ErrBusy) {
			busy = true
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if !busy {
		t.Fatal("expected ErrBusy when buffer is full and handler is slow")
	}
}

func TestPublishAfterClose(t *testing.T) {
	l := New(config.LocalConfig{BufferSize: 4, PublishTimeoutMS: 10}, 0)
	ctx := context.Background()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if err := l.Publish(ctx, &model.Envelope{}); !errors.Is(err, transport.ErrClosed) {
		t.Fatalf("got %v, want ErrClosed", err)
	}
}
