package service

import (
	"context"
	"log"
)

// PresenceStore 由 presence 包实现；对 service 来说它就是 dao。
type PresenceStore interface {
	Batch(ctx context.Context, uids []int64) (map[int64]bool, error)
}

type PresenceService struct {
	store PresenceStore
}

func NewPresenceService(store PresenceStore) *PresenceService {
	return &PresenceService{store: store}
}

func (s *PresenceService) Query(ctx context.Context, uids []int64) (map[int64]bool, error) {
	if len(uids) > 500 {
		return nil, newErr(CodeBadRequest, "at most 500 uids")
	}
	m, err := s.store.Batch(ctx, uids)
	if err != nil {
		log.Printf("presence batch: %v", err)
		return nil, newErr(CodeUnavailable, "presence unavailable")
	}
	return m, nil
}
