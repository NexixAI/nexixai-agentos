package storage

import "context"

// KVStore is a tenant-scoped persistence port for key-value state.
type KVStore interface {
	Get(ctx context.Context, tenantID, agentID, key string) (string, bool, error)
	Set(ctx context.Context, tenantID, agentID, key, value string) error
	Delete(ctx context.Context, tenantID, agentID, key string) error
	ListKeys(ctx context.Context, tenantID, agentID string) ([]string, error)
	Close() error
}
