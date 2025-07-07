package oracle

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const (
	defaultMaxPhaseOutputBytes     = 1_000_000 // 1 MB
	defaultMaxReportCount          = 20
	defaultBatchSize               = 20
	defaultOutcomePruningThreshold = 3600
	defaultRequestExpiry           = 20 * time.Second
)

type SetRequestTimeout func(timeout time.Duration)

type factory struct {
	store *requests.Store[*ConsensusRequest]

	// Request timeout is set by the plugin factory and used by the reporting plugin to set the timeout for requests
	// created in the capability
	setRequestTimeout SetRequestTimeout
	batchSize         int
	lggr              logger.Logger

	services.StateMachine
}

func NewReportingPluginFactory(lggr logger.Logger, s *requests.Store[*ConsensusRequest],
	setRequestTimeout SetRequestTimeout, batchSize int) (*factory, error) {
	return &factory{
		store:             s,
		setRequestTimeout: setRequestTimeout,
		batchSize:         batchSize,
		lggr:              logger.Named(lggr, "ConsensusCapabilityPluginFactory"),
	}, nil
}

func (o *factory) NewReportingPlugin(_ context.Context, config ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[[]byte], ocr3types.ReportingPluginInfo, error) {
	var configProto types.ReportingPluginConfig
	err := proto.Unmarshal(config.OffchainConfig, &configProto)
	if err != nil {
		// an empty byte array will be unmarshalled into zero values without error
		return nil, ocr3types.ReportingPluginInfo{}, err
	}
	if configProto.MaxQueryLengthBytes <= 0 {
		configProto.MaxQueryLengthBytes = defaultMaxPhaseOutputBytes
	}
	if configProto.MaxObservationLengthBytes <= 0 {
		configProto.MaxObservationLengthBytes = defaultMaxPhaseOutputBytes
	}
	if configProto.MaxOutcomeLengthBytes <= 0 {
		configProto.MaxOutcomeLengthBytes = defaultMaxPhaseOutputBytes
	}
	if configProto.MaxReportLengthBytes <= 0 {
		configProto.MaxReportLengthBytes = defaultMaxPhaseOutputBytes
	}
	if configProto.MaxBatchSize <= 0 {
		configProto.MaxBatchSize = defaultBatchSize
	}
	if configProto.OutcomePruningThreshold <= 0 {
		configProto.OutcomePruningThreshold = defaultOutcomePruningThreshold
	}
	if configProto.MaxReportCount <= 0 {
		configProto.MaxReportCount = defaultMaxReportCount
	}
	if configProto.RequestTimeout == nil {
		configProto.RequestTimeout = durationpb.New(defaultRequestExpiry)
	}
	o.setRequestTimeout(configProto.RequestTimeout.AsDuration())
	rp, err := NewReportingPlugin(o.lggr, config.F, config.N, o.store, int(configProto.MaxBatchSize))
	rpInfo := ocr3types.ReportingPluginInfo{
		Name: "Consensus Capability Plugin",
		Limits: ocr3types.ReportingPluginLimits{
			MaxQueryLength:       int(configProto.MaxQueryLengthBytes),
			MaxObservationLength: int(configProto.MaxObservationLengthBytes),
			MaxOutcomeLength:     int(configProto.MaxOutcomeLengthBytes),
			MaxReportLength:      int(configProto.MaxReportLengthBytes),
			MaxReportCount:       int(configProto.MaxReportCount),
		},
	}
	return rp, rpInfo, err
}

func (o *factory) Start(ctx context.Context) error {
	return o.StartOnce("ConsensusCapabilityReportingPlugin", func() error {
		return nil
	})
}

func (o *factory) Close() error {
	return o.StopOnce("ConsensusCapabilityReportingPlugin", func() error {
		return nil
	})
}

func (o *factory) Name() string { return o.lggr.Name() }

func (o *factory) HealthReport() map[string]error {
	return map[string]error{o.Name(): o.Healthy()}
}
