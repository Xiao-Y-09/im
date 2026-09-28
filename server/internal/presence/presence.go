package presence

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const ttl = 90 * time.Second

// 只有值仍是本实例 ID 时才删除：用户可能已经重连到另一个实例并写入了新值。
var delIfOwner = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`)

// Store 记录“用户连在哪个实例上”，所有实例共享，因此能回答跨实例的在线查询。
type Store struct {
	rdb        *redis.Client
	instanceID string
}

func New(rdb *redis.Client, instanceID string) *Store {
	return &Store{rdb: rdb, instanceID: instanceID}
}

func key(uid int64) string {
	return "online:" + strconv.FormatInt(uid, 10)
}

func (s *Store) Online(ctx context.Context, uid int64) error {
	return s.rdb.Set(ctx, key(uid), s.instanceID, ttl).Err()
}

// Refresh 由 writePump 每次发 ping 时调用。key 不存在（比如被竞争中的 Offline 删掉了）时补写，
// 否则一个仍然在线的用户会一直显示离线直到重连。
func (s *Store) Refresh(ctx context.Context, uid int64) error {
	ok, err := s.rdb.Expire(ctx, key(uid), ttl).Result()
	if err != nil {
		return err
	}
	if !ok {
		return s.Online(ctx, uid)
	}
	return nil
}

func (s *Store) Offline(ctx context.Context, uid int64) error {
	return delIfOwner.Run(ctx, s.rdb, []string{key(uid)}, s.instanceID).Err()
}

func (s *Store) IsOnline(ctx context.Context, uid int64) (bool, error) {
	n, err := s.rdb.Exists(ctx, key(uid)).Result()
	return n > 0, err
}

func (s *Store) Batch(ctx context.Context, uids []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	keys := make([]string, len(uids))
	for i, uid := range uids {
		keys[i] = key(uid)
	}
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, uid := range uids {
		out[uid] = vals[i] != nil
	}
	return out, nil
}
