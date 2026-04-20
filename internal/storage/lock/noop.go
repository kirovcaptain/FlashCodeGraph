// Package lock provides IndexLock implementations.
package lock

import (
	"context"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// NoopLock is a no-op lock for local mode.
type NoopLock struct{}

// NewNoopLock creates a no-op lock.
func NewNoopLock() *NoopLock { return &NoopLock{} }

func (lock *NoopLock) Acquire(_ context.Context, _, _, _ string, _ time.Duration) error { return nil }
func (lock *NoopLock) Release(_ context.Context, _, _ string) error           { return nil }
func (lock *NoopLock) ForceRelease(_ context.Context, _, _ string) error      { return nil }
func (lock *NoopLock) Query(_ context.Context, _, _ string) (*model.LockInfo, error) {
	return nil, nil
}
