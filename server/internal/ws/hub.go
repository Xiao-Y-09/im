package ws

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"im/internal/metrics"
	"im/internal/model"
)

// 群成员缓存除了 Join 时主动失效，还有一个短 TTL：
// 多实例下别的实例上的 Join 只会让那个实例的缓存失效，TTL 保证这里最多滞后几秒。
const (
	groupCacheTTL   = 5 * time.Second
	presenceTimeout = 2 * time.Second
)

type MemberSource interface {
	MemberIDs(groupID int64) ([]int64, error)
}

type Presence interface {
	Online(ctx context.Context, uid int64) error
	Refresh(ctx context.Context, uid int64) error
	Offline(ctx context.Context, uid int64) error
}

type groupEntry struct {
	members  []int64
	loadedAt time.Time
}

// Hub 记录本进程上的连接。锁的约定：只在持写锁时 close(c.send)，只在持读锁时往 c.send 写，
// 所以永远不会向已关闭的 channel 发送。
type Hub struct {
	mu      sync.RWMutex
	clients map[int64]map[*Client]struct{} // 一个用户可以多个 tab

	members  MemberSource
	presence Presence

	gmu      sync.Mutex
	groups   map[int64]groupEntry
	groupGen map[int64]uint64
}

func NewHub(members MemberSource, presence Presence) *Hub {
	return &Hub{
		clients:  make(map[int64]map[*Client]struct{}),
		members:  members,
		presence: presence,
		groups:   make(map[int64]groupEntry),
		groupGen: make(map[int64]uint64),
	}
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	set := h.clients[c.UserID]
	if set == nil {
		set = make(map[*Client]struct{})
		h.clients[c.UserID] = set
	}
	set[c] = struct{}{}
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), presenceTimeout)
	defer cancel()
	if err := h.presence.Online(ctx, c.UserID); err != nil {
		log.Printf("presence online uid=%d: %v", c.UserID, err)
	}
}

// Unregister 幂等；可能在 dispatcher 里被调用（踢慢客户端），所以 Redis 操作放到后台做。
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	set, ok := h.clients[c.UserID]
	if !ok {
		h.mu.Unlock()
		return
	}
	if _, ok := set[c]; !ok {
		h.mu.Unlock()
		return
	}
	delete(set, c)
	close(c.send)
	last := len(set) == 0
	if last {
		delete(h.clients, c.UserID)
	}
	h.mu.Unlock()

	if last {
		go h.markOffline(c.UserID)
	}
}

func (h *Hub) markOffline(uid int64) {
	// 异步期间用户可能又连上来了，那就不能删
	h.mu.RLock()
	_, back := h.clients[uid]
	h.mu.RUnlock()
	if back {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), presenceTimeout)
	defer cancel()
	if err := h.presence.Offline(ctx, uid); err != nil {
		log.Printf("presence offline uid=%d: %v", uid, err)
	}
}

func (h *Hub) refreshPresence(uid int64) {
	ctx, cancel := context.WithTimeout(context.Background(), presenceTimeout)
	defer cancel()
	if err := h.presence.Refresh(ctx, uid); err != nil {
		log.Printf("presence refresh uid=%d: %v", uid, err)
	}
}

// Deliver 是 Transport 的 "deliver" Handler：把消息推给连在本进程上的目标用户。
// 它从不阻塞在某个连接上——写不进 send 的慢客户端直接踢掉。
func (h *Hub) Deliver(env *model.Envelope) {
	targets, err := h.targets(env)
	if err != nil {
		log.Printf("deliver seq=%d conv=%s: %v", env.Seq, env.ConvID, err)
		return
	}
	if len(targets) == 0 {
		return
	}
	b, err := json.Marshal(env)
	if err != nil {
		log.Printf("deliver marshal seq=%d: %v", env.Seq, err)
		return
	}

	var slow []*Client
	h.mu.RLock()
	for _, uid := range targets {
		for c := range h.clients[uid] {
			select {
			case c.send <- b:
				metrics.Delivered.Add(1)
			default:
				slow = append(slow, c)
			}
		}
	}
	h.mu.RUnlock()

	h.kick(slow)
}

func (h *Hub) targets(env *model.Envelope) ([]int64, error) {
	if model.IsCallType(env.Type) {
		return []int64{env.To}, nil
	}
	if a, b, ok := model.ParsePrivateConvID(env.ConvID); ok {
		return []int64{a, b}, nil // 发送者也在里面，这就是回显确认
	}
	if gid, ok := model.ParseGroupConvID(env.ConvID); ok {
		return h.groupMembers(gid)
	}
	return nil, nil
}

// SendTo 只发给某一个连接（错误回复等），同样遵守非阻塞 + 踢慢客户端。
func (h *Hub) SendTo(c *Client, b []byte) {
	h.mu.RLock()
	_, registered := h.clients[c.UserID][c]
	full := false
	if registered {
		select {
		case c.send <- b:
		default:
			full = true
		}
	}
	h.mu.RUnlock()
	if full {
		h.kick([]*Client{c})
	}
}

func (h *Hub) kick(slow []*Client) {
	for _, c := range slow {
		metrics.SlowKicked.Add(1)
		log.Printf("kick slow client uid=%d: send buffer full", c.UserID)
		h.Unregister(c)
	}
}

func (h *Hub) groupMembers(gid int64) ([]int64, error) {
	h.gmu.Lock()
	e, ok := h.groups[gid]
	gen := h.groupGen[gid]
	h.gmu.Unlock()
	if ok && time.Since(e.loadedAt) < groupCacheTTL {
		return e.members, nil
	}

	ids, err := h.members.MemberIDs(gid)
	if err != nil {
		return nil, err
	}

	h.gmu.Lock()
	// 查询期间发生过 Invalidate，说明结果可能已过期，这次用但不缓存
	if h.groupGen[gid] == gen {
		h.groups[gid] = groupEntry{members: ids, loadedAt: time.Now()}
	}
	h.gmu.Unlock()
	return ids, nil
}

func (h *Hub) InvalidateGroup(gid int64) {
	h.gmu.Lock()
	delete(h.groups, gid)
	h.groupGen[gid]++
	h.gmu.Unlock()
}

// CloseAll 在优雅退出时断开所有连接，并清掉本实例写入的在线状态。
func (h *Hub) CloseAll() {
	h.mu.Lock()
	uids := make([]int64, 0, len(h.clients))
	for uid, set := range h.clients {
		for c := range set {
			close(c.send)
		}
		uids = append(uids, uid)
	}
	h.clients = make(map[int64]map[*Client]struct{})
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, uid := range uids {
		if err := h.presence.Offline(ctx, uid); err != nil {
			log.Printf("presence offline uid=%d: %v", uid, err)
		}
	}
}
