package plugin

import (
	"time"

	"github.com/buraksezer/consistent"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/capabilities/ring/internal/environment"
	"github.com/smartcontractkit/capabilities/ring/internal/pb"
	"github.com/smartcontractkit/capabilities/ring/internal/request"
	"github.com/smartcontractkit/capabilities/ring/internal/rings"
)

// PluginFactory creates new ring plugin instances
type PluginFactory struct {
	store          *requests.Store[*request.Request]
	scaler         environment.Scaler
	requestTimeout time.Duration
	timeToSync     time.Duration
	f              int
}

// NewPluginFactory creates a new plugin factory
func NewPluginFactory(
	store *requests.Store[*request.Request],
	scaler environment.Scaler,
	requestTimeout, timeToSync time.Duration,
	f int) *PluginFactory {
	return &PluginFactory{
		store:          store,
		scaler:         scaler,
		requestTimeout: requestTimeout,
		timeToSync:     timeToSync,
		f:              f,
	}
}

// NewReportingPlugin creates a new reporting plugin instance
func (f *PluginFactory) NewReportingPlugin(config ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[*pb.Outcome], error) {
	hashConfig := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            rings.NewHasher(),
	}

	return NewRingPlugin(
		f.store,
		f.scaler,
		f.requestTimeout,
		f.timeToSync,
		f.f,
		hashConfig,
	), nil
}
