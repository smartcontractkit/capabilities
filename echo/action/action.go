package action

import (
	"context"
	"fmt"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/capabilities/echo/shared"
	"github.com/smartcontractkit/capabilities/echo/store"
	"github.com/smartcontractkit/capabilities/echo/utils"
)

var (
	ID         = "echo-action@1.0.0"
	actionInfo = capabilities.MustNewCapabilityInfo(
		ID,
		capabilities.CapabilityTypeAction,
		"A simple capability that stores and echoes back a key and value.",
	)
)

// durableResourceEmitter adapts beholder.Emitter to shared.Emitter interface.
// rename: durableMessageEmitter
type durableResourceEmitter struct{}

func (b *durableResourceEmitter) Emit(ctx context.Context, data []byte) error {
	return beholder.GetEmitter().Emit(ctx, data)
}

type capability struct {
	services.StateMachine //consider embedding service engine

	lggr            logger.Logger
	store           *store.Store
	resourceManager *shared.ResourceManager
}

// Options for configuring the capability
type Options struct {
	// Clock is optional, defaults to real clock. Inject fake clock for testing.
	Clock clockwork.Clock

	// SnapshotInterval is how often to emit utilization snapshots.
	SnapshotInterval time.Duration

	// Emitter is optional, defaults to beholder. Inject mock for testing.
	Emitter shared.Emitter

	// ResourceInfo provides metadata for metering. Has sensible defaults.
	ResourceInfo shared.ResourceInfo
}

// DefaultOptions returns default options
func DefaultOptions() Options {
	return Options{
		Clock:            nil, // will use real clock
		SnapshotInterval: time.Second,
		Emitter:          nil, // will use beholder
		ResourceInfo: shared.ResourceInfo{
			Entity:       "echo-action",
			Resource:     "echo-action-filestore",
			ResourceType: "filestore",
		},
	}
}

func New(lggr logger.Logger, storageDir string, opts ...Options) (*capability, error) {
	opt := DefaultOptions()
	if len(opts) > 0 {
		provided := opts[0]
		// Merge provided options with defaults (only override non-zero values)
		if provided.Clock != nil {
			opt.Clock = provided.Clock
		}
		if provided.SnapshotInterval != 0 {
			opt.SnapshotInterval = provided.SnapshotInterval
		}
		if provided.Emitter != nil {
			opt.Emitter = provided.Emitter
		}
		if provided.ResourceInfo.Entity != "" {
			opt.ResourceInfo.Entity = provided.ResourceInfo.Entity
		}
		if provided.ResourceInfo.Resource != "" {
			opt.ResourceInfo.Resource = provided.ResourceInfo.Resource
		}
		if provided.ResourceInfo.ResourceType != "" {
			opt.ResourceInfo.ResourceType = provided.ResourceInfo.ResourceType
		}
	}

	// Use beholder if no emitter provided
	emitter := opt.Emitter
	if emitter == nil {
		emitter = &durableResourceEmitter{}
	}

	// Create store with resource info for metering
	s, err := store.New(store.StoreConfig{
		Dir:          storageDir,
		ResourceInfo: opt.ResourceInfo,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	// Create ResourceManager and register the store as a Meterable
	rm := shared.NewResourceManager(lggr, shared.ResourceManagerConfig{
		SnapshotInterval: opt.SnapshotInterval,
		Clock:            opt.Clock,
		Emitter:          emitter,
	})
	rm.Register(s)

	return &capability{
		lggr:            lggr,
		store:           s,
		resourceManager: rm,
	}, nil
}

func (c *capability) Info(_ context.Context) (capabilities.CapabilityInfo, error) {
	return actionInfo, nil
}

func (c *capability) Start(ctx context.Context) error {
	return c.StartOnce("EchoCapability", func() error {
		return c.resourceManager.Start(ctx)
	})
}

func (c *capability) Close() error {
	return c.StopOnce("EchoCapability", func() error {
		return c.resourceManager.Close()
	})
}

func (c *capability) Ready() error { //same as below
	return nil
}

func (c *capability) HealthReport() map[string]error { //same as below
	return map[string]error{c.Name(): c.Healthy()}
}

func (c *capability) Name() string { //could be saved if use services.ServiceEngine
	return "EchoCapability"
}

func (c *capability) Execute(ctx context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	// Start ResourceManager on first execute (idempotent via StartOnce)
	if err := c.Start(ctx); err != nil {
		c.lggr.Errorw("failed to start resource manager", "error", err)
	}

	return capabilities.Execute(ctx, request,
		&shared.Request{},
		&shared.Config{},
		func(ctx context.Context, metadata capabilities.RequestMetadata, input *shared.Request, config *shared.Config) (*shared.Response, capabilities.ResponseMetadata, error) {
			c.lggr.Debugw("executing echo action",
				"workflowID", metadata.WorkflowID,
				"executionID", metadata.WorkflowExecutionID,
				"key", input.Key,
			)

			// Validate inputs
			if err := utils.ValidateKey(input.Key); err != nil {
				return nil, capabilities.ResponseMetadata{}, err
			}
			if err := utils.ValidateValue(input.Value); err != nil {
				return nil, capabilities.ResponseMetadata{}, err
			}

			// Build the stored key: <workflowID>:<key>:<value>
			storedKey := fmt.Sprintf("%s:%s:%s", metadata.WorkflowID, input.Key, input.Value)

			// Create and store the record
			record := &store.Record{
				Key:           input.Key,
				Value:         input.Value,
				StoredKey:     storedKey,
				LastUpdatedBy: metadata.WorkflowExecutionID,
				LastUpdatedAt: time.Now().UTC(),
				WorkflowID:    metadata.WorkflowID,
				Owner:         metadata.WorkflowOwner,
			}

			if err := c.store.Put(storedKey, record); err != nil {
				return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to store record: %w", err)
			}

			c.lggr.Infow("stored record",
				"storedKey", storedKey,
				"lastUpdatedBy", metadata.WorkflowExecutionID,
			)

			return &shared.Response{
				StoredKey: storedKey,
				Key:       input.Key,
				Value:     input.Value,
			}, capabilities.ResponseMetadata{}, nil
		})
}

func (c *capability) RegisterToWorkflow(_ context.Context, _ capabilities.RegisterToWorkflowRequest) error {
	return nil
}

func (c *capability) UnregisterFromWorkflow(_ context.Context, _ capabilities.UnregisterFromWorkflowRequest) error {
	return nil
}
