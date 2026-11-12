// Package cre is a minimal in-process stand-in for github.com/smartcontractkit/cre-sdk-go/cre. It mirrors the SDK API
// the example workflow uses, so that workflow code can run natively against the real capability in tests.
package cre

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

type Runtime interface {
	Logger() *slog.Logger
	Context() context.Context
	Metadata() capabilities.RequestMetadata
	NumNodes() int
	Capability(id string) any
}

// TestRuntime runs DON-mode callbacks once and node-mode callbacks once per node, sequentially.
type TestRuntime struct {
	Ctx           context.Context //nolint:containedctx // The runtime is the context the workflow runs in.
	Lggr          *slog.Logger
	RequestMeta   capabilities.RequestMetadata
	Nodes         int
	CapabilityMap map[string]any
}

var _ Runtime = (*TestRuntime)(nil)

func (r *TestRuntime) Logger() *slog.Logger                   { return r.Lggr }
func (r *TestRuntime) Context() context.Context               { return r.Ctx }
func (r *TestRuntime) Metadata() capabilities.RequestMetadata { return r.RequestMeta }
func (r *TestRuntime) NumNodes() int                          { return r.Nodes }
func (r *TestRuntime) Capability(id string) any               { return r.CapabilityMap[id] }

type Promise[T any] interface {
	Await() (T, error)
}

type promise[T any] func() (T, error)

func (p promise[T]) Await() (T, error) { return p() }

func NewPromise[T any](await func() (T, error)) Promise[T] { return promise[T](await) }

type Trigger[T any] interface {
	ID() string
}

type ExecutionHandler[C any] interface {
	TriggerID() string
	Execute(config C, runtime Runtime, payload any) (any, error)
}

type handler[C, T, O any] struct {
	trigger  Trigger[T]
	callback func(C, Runtime, T) (O, error)
}

func (h handler[C, T, O]) TriggerID() string { return h.trigger.ID() }

func (h handler[C, T, O]) Execute(config C, runtime Runtime, payload any) (any, error) {
	p, ok := payload.(T)
	if !ok {
		return nil, fmt.Errorf("unexpected trigger payload type %T", payload)
	}

	return h.callback(config, runtime, p)
}

func Handler[C, T, O any](trigger Trigger[T], callback func(C, Runtime, T) (O, error)) ExecutionHandler[C] {
	return handler[C, T, O]{trigger: trigger, callback: callback}
}

type Workflow[C any] []ExecutionHandler[C]

func ParseJSON[C any](b []byte) (*C, error) {
	c := new(C)
	return c, json.Unmarshal(b, c)
}

type ConsensusAggregation[T any] struct {
	Aggregate func(observations []T) (T, error)
}

// ConsensusMedianAggregation returns the upper median of decimal observations.
func ConsensusMedianAggregation() ConsensusAggregation[decimal.Decimal] {
	return ConsensusAggregation[decimal.Decimal]{Aggregate: func(observations []decimal.Decimal) (decimal.Decimal, error) {
		if len(observations) == 0 {
			return decimal.Decimal{}, errors.New("no observations")
		}

		sorted := slices.Clone(observations)
		slices.SortFunc(sorted, decimal.Decimal.Cmp)
		return sorted[len(sorted)/2], nil
	}}
}
