package testutils

import (
	"context"
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var _ core.OracleFactory = (*oracleFactory)(nil)

type oracleFactory struct {
	t *testing.T
}

func NewOracleFactory(t *testing.T) *oracleFactory {
	return &oracleFactory{
		t: t,
	}
}

// NewOracle(ctx context.Context, args OracleArgs) (Oracle, error)
func (of *oracleFactory) NewOracle(ctx context.Context, args core.OracleArgs) (core.Oracle, error) {
	return &oracle{}, nil
}

type oracle struct{}

func (o *oracle) Start(ctx context.Context) error {
	return nil
}

func (o *oracle) Close(ctx context.Context) error {
	return nil
}
