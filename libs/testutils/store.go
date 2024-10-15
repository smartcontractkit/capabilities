package testutils

import (
	"context"
	"fmt"
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var _ core.KeyValueStore = (*store)(nil)

type store struct {
	t      *testing.T
	values map[string][]byte
}

func NewStore(t *testing.T) *store {
	return &store{
		t:      t,
		values: make(map[string][]byte),
	}
}

func (ts *store) Store(_ context.Context, key string, value []byte) error {
	ts.values[key] = value
	return nil
}

func (ts *store) Get(_ context.Context, key string) ([]byte, error) {
	if _, ok := ts.values[key]; !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return ts.values[key], nil
}
