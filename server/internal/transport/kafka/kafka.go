package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"im/internal/config"
	"im/internal/model"
	"im/internal/transport"
)

type subscriber struct {
	name   string
	reader *kafkago.Reader
	h      transport.Handler
}

// Kafka 用单 partition topic 做跨实例消息总线：partition 内 offset 全局有序，
// 所以 seq = offset + 1 在所有实例上一致，不需要额外的发号器。
type Kafka struct {
	cfg        config.KafkaConfig
	instanceID string
	writer     *kafkago.Writer

	mu      sync.Mutex
	subs    []*subscriber
	started bool
	closed  bool

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ transport.Transport = (*Kafka)(nil)

func New(cfg config.KafkaConfig, instanceID string) *Kafka {
	return &Kafka{
		cfg:        cfg,
		instanceID: instanceID,
		writer: &kafkago.Writer{
			Addr:         kafkago.TCP(cfg.Brokers...),
			Topic:        cfg.Topic,
			RequiredAcks: kafkago.RequireAll,
			BatchTimeout: 5 * time.Millisecond, // 默认 1s，不改会导致每条消息延迟约 1 秒
			BatchSize:    500,
		},
	}
}

func (k *Kafka) Publish(ctx context.Context, env *model.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return k.writer.WriteMessages(ctx, kafkago.Message{Key: []byte(env.ConvID), Value: b})
}

// Subscribe 用 consumer group 实现两种语义：
// 每个实例独立的 group → 每个实例各收一份（投递）；所有实例共享一个 group → 全局只收一份（落库）。
func (k *Kafka) Subscribe(_ context.Context, sub transport.Subscription, h transport.Handler) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.started {
		return errors.New("kafka transport: Subscribe after Start")
	}
	groupID := "im-" + sub.Name
	if sub.Broadcast {
		groupID += "-" + k.instanceID
	}
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: k.cfg.Brokers,
		Topic:   k.cfg.Topic,
		GroupID: groupID,
		// ClientID 带上实例名，kafka-consumer-groups.sh --describe 才能看出分区在哪个实例手里
		Dialer:      &kafkago.Dialer{ClientID: "im-" + k.instanceID, Timeout: 10 * time.Second, DualStack: true},
		StartOffset: kafkago.LastOffset, // 新 group 不重放历史；历史走 MySQL
		MinBytes:    1,
		MaxBytes:    10 << 20,
		MaxWait:     100 * time.Millisecond,
		// 同步逐条 commit 每条消息一次网络往返，会把吞吐压到约 1k/s。
		// 改成每秒批量提交：崩溃时最多重放 1 秒的消息，靠 INSERT IGNORE 和前端按 seq 去重兜住。
		CommitInterval: time.Second,
	})
	k.subs = append(k.subs, &subscriber{name: groupID, reader: r, h: h})
	return nil
}

func (k *Kafka) Start(ctx context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.started {
		return errors.New("kafka transport: already started")
	}
	k.started = true
	runCtx, cancel := context.WithCancel(ctx)
	k.cancel = cancel
	for _, s := range k.subs {
		k.wg.Add(1)
		go k.consume(runCtx, s)
	}
	return nil
}

func (k *Kafka) consume(ctx context.Context, s *subscriber) {
	defer k.wg.Done()
	for {
		m, err := s.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return
			}
			log.Printf("kafka[%s] fetch: %v", s.name, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}

		var env model.Envelope
		if err := json.Unmarshal(m.Value, &env); err != nil {
			log.Printf("kafka[%s] skip bad message at offset %d: %v", s.name, m.Offset, err)
		} else {
			env.Seq = m.Offset + 1
			s.h(&env)
		}

		if err := s.reader.CommitMessages(ctx, m); err != nil && ctx.Err() == nil {
			log.Printf("kafka[%s] commit offset %d: %v", s.name, m.Offset, err)
		}
	}
}

func (k *Kafka) Close() error {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return nil
	}
	k.closed = true
	if k.cancel != nil {
		k.cancel()
	}
	k.mu.Unlock()

	k.wg.Wait()
	var errs []error
	// Reader.Close 会把 CommitInterval 内还没提交的 offset 刷掉
	for _, s := range k.subs {
		if err := s.reader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close reader %s: %w", s.name, err))
		}
	}
	if err := k.writer.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close writer: %w", err))
	}
	return errors.Join(errs...)
}
