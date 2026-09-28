package persist

import (
	"log"
	"sync"
	"time"

	"im/internal/metrics"
	"im/internal/model"
)

const (
	queueSize     = 8192
	batchSize     = 200
	flushInterval = 50 * time.Millisecond
	maxAttempts   = 3
)

type MessageStore interface {
	BatchInsertIgnore(msgs []model.Message) error
}

// Persister 把消息攒批写库。它有自己的队列，这样 MySQL 变慢时只会丢落库（并计数），
// 不会反过来卡住 Transport 的 dispatcher 和实时投递。
type Persister struct {
	store MessageStore
	ch    chan *model.Envelope

	quit      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	lastDropLog time.Time
	dropMu      sync.Mutex
}

func New(store MessageStore) *Persister {
	p := &Persister{
		store: store,
		ch:    make(chan *model.Envelope, queueSize),
		quit:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go p.run()
	return p
}

// Enqueue 是 Transport Handler，绝不阻塞。
func (p *Persister) Enqueue(env *model.Envelope) {
	if env.Type != model.TypeChat && env.Type != model.TypeFile {
		return // call_* 信令是瞬时的，不落库
	}
	select {
	case p.ch <- env:
	default:
		metrics.PersistDropped.Add(1)
		p.logDrop(env)
	}
}

// 队列满时每秒最多打一行日志，否则日志本身会成为新的瓶颈。
func (p *Persister) logDrop(env *model.Envelope) {
	p.dropMu.Lock()
	defer p.dropMu.Unlock()
	if time.Since(p.lastDropLog) >= time.Second {
		p.lastDropLog = time.Now()
		log.Printf("persist queue full, dropping seq=%d (total dropped=%d)", env.Seq, metrics.PersistDropped.Value())
	}
}

func (p *Persister) run() {
	defer close(p.done)
	batch := make([]*model.Envelope, 0, batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case env := <-p.ch:
			batch = append(batch, env)
			if len(batch) >= batchSize {
				batch = p.flush(batch)
			}
		case <-ticker.C:
			batch = p.flush(batch)
		case <-p.quit:
			for {
				select {
				case env := <-p.ch:
					batch = append(batch, env)
					if len(batch) >= batchSize {
						batch = p.flush(batch)
					}
				default:
					p.flush(batch)
					return
				}
			}
		}
	}
}

func (p *Persister) flush(batch []*model.Envelope) []*model.Envelope {
	if len(batch) == 0 {
		return batch
	}
	rows := make([]model.Message, len(batch))
	for i, env := range batch {
		rows[i] = model.Message{
			Seq:         env.Seq,
			ConvID:      env.ConvID,
			SenderID:    env.From,
			Type:        env.Type,
			Content:     string(env.Payload),
			ClientMsgID: env.ClientMsgID,
			CreatedAt:   time.UnixMilli(env.TS),
		}
	}

	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err = p.store.BatchInsertIgnore(rows); err == nil {
			metrics.Persisted.Add(int64(len(rows)))
			break
		}
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
	}
	if err != nil {
		log.Printf("persist batch of %d (seq %d..%d) failed after %d attempts: %v",
			len(rows), rows[0].Seq, rows[len(rows)-1].Seq, maxAttempts, err)
	}
	return batch[:0]
}

// Close 把队列里剩下的消息写完再返回。调用前应先关闭 Transport，确保不再有新消息进来。
func (p *Persister) Close() {
	p.closeOnce.Do(func() { close(p.quit) })
	<-p.done
}
