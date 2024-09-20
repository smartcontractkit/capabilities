package mocks

import (
	"context"
	"sync"
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

var (
	_ capabilities.ActionCapability = &mockTarget{}
)

const mockTargetID = "mock-target@1.0.0"

type TargetSink struct {
	services.StateMachine
	targets []mockTarget
	Sink    chan capabilities.CapabilityRequest

	stopCh services.StopChan
	wg     sync.WaitGroup
}

func NewTargetSink() *TargetSink {
	return &TargetSink{
		Sink:   make(chan capabilities.CapabilityRequest, 1000),
		stopCh: make(services.StopChan),
	}
}

func (ts *TargetSink) Start(ctx context.Context) error {
	return ts.StartOnce("TargetSinkService", func() error {
		return nil
	})
}

func (ts *TargetSink) Close() error {
	return ts.StopOnce("TargetSinkService", func() error {
		close(ts.stopCh)
		ts.wg.Wait()
		return nil
	})
}

func (ts *TargetSink) GetNewTarget(t *testing.T) *mockTarget {
	target := mockTarget{
		t:      t,
		ch:     ts.Sink,
		wg:     &ts.wg,
		stopCh: ts.stopCh,
	}
	ts.targets = append(ts.targets, target)
	return &target
}

type mockTarget struct {
	t      *testing.T
	cancel context.CancelFunc
	ch     chan capabilities.CapabilityRequest

	wg     *sync.WaitGroup
	stopCh services.StopChan
}

func (mt *mockTarget) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	mt.ch <- rawRequest
	return capabilities.CapabilityResponse{}, nil
}

func (mt *mockTarget) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.MustNewCapabilityInfo(
		mockTargetID,
		capabilities.CapabilityTypeTarget,
		"mocks a target capability.",
	), nil
}

func (mt *mockTarget) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	return nil
}

func (mt *mockTarget) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	return nil
}
