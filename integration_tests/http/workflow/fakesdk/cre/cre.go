// Package cre is a minimal in-process stand-in for github.com/smartcontractkit/cre-sdk-go/cre. It mirrors the SDK API
// used by the getting-started workflows, so that workflow code can run natively against real capabilities in tests.
package cre

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"sort"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

type SecretsProvider any

type Runtime interface {
	Logger() *slog.Logger
	Context() context.Context
	Metadata() capabilities.RequestMetadata
	NumNodes() int
	Capability(id string) any
}

// TestRuntime runs DON-mode callbacks once and node-mode callbacks once per node, sequentially.
type TestRuntime struct {
	Ctx           context.Context
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

// ConsensusMedianAggregation returns the upper median. Only *big.Int is supported.
func ConsensusMedianAggregation[T any]() ConsensusAggregation[T] {
	return ConsensusAggregation[T]{Aggregate: func(observations []T) (T, error) {
		var zero T
		if len(observations) == 0 {
			return zero, fmt.Errorf("no observations")
		}
		sorted := append([]T(nil), observations...)
		for _, o := range sorted {
			if _, ok := any(o).(*big.Int); !ok {
				return zero, fmt.Errorf("median aggregation not supported for %T", o)
			}
		}
		sort.Slice(sorted, func(i, j int) bool {
			return any(sorted[i]).(*big.Int).Cmp(any(sorted[j]).(*big.Int)) < 0
		})
		return sorted[len(sorted)/2], nil
	}}
}
