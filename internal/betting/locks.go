package betting

import (
	"context"
	"sync"
)

// playerLocks serializes the operations of one player (decisions.md).
// Each lock is a channel with room for one token; waiting is bounded by ctx.
type playerLocks struct {
	mu    sync.Mutex
	locks map[int64]chan struct{}
}

func newPlayerLocks() *playerLocks {
	return &playerLocks{locks: make(map[int64]chan struct{})}
}

func (p *playerLocks) lock(ctx context.Context, userID int64) (unlock func(), err error) {
	p.mu.Lock()
	ch, ok := p.locks[userID]
	if !ok {
		ch = make(chan struct{}, 1)
		p.locks[userID] = ch
	}
	p.mu.Unlock()

	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
