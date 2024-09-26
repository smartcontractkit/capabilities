package main

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
)

var _ core.KeyValueStore = (*store)(nil)

type store struct {
	logger   logger.Logger
	internal map[string]any
}

func NewStore(lggr logger.Logger) *store {
	return &store{
		logger:   lggr,
		internal: make(map[string]any),
	}
}

func (s *store) Store(_ context.Context, key string, val []byte) error {
	s.logger.Debugf("writing %v to %s", val, key)
	s.internal[key] = val
	return nil
}

func (s *store) Get(_ context.Context, key string) ([]byte, error) {
	found, ok := s.internal[key]
	if !ok {
		return nil, fmt.Errorf("value not found for key")
	}
	return found.([]byte), nil
}
