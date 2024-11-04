package trigger

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common" // nolint:depguard
	"github.com/ethereum/go-ethereum/crypto" //nolint:depguard
	"github.com/smartcontractkit/libocr/offchainreporting2/chains/evmutil"
	ocrTypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/datastreams"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	v3 "github.com/smartcontractkit/chainlink-common/pkg/types/mercury/v3"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/streams/streamscap"
	"github.com/smartcontractkit/capabilities/streams/trigger/reportcodec"
)

const defaultSendChannelBufferSize = 1000
const defaultTickerResolutionMs = 100

type CapabilityService interface {
	services.Service
	capabilities.TriggerCapability
}

var cronTriggerInfo = capabilities.MustNewCapabilityInfo(
	"mock-streams-trigger@1.0.0",
	capabilities.CapabilityTypeTrigger,
	"A trigger that periodically returns a mock streams report.",
)

var _ CapabilityService = (*capability)(nil)

type subscriber struct {
	Ch                               chan<- capabilities.TriggerResponse
	WorkflowID                       string
	Config                           streamscap.TriggerConfig
	DurationSinceLastTriggerResponse int
}

type capability struct {
	lggr                  logger.Logger
	meta                  datastreams.Metadata
	mu                    sync.Mutex
	sendChannelBufferSize int
	signers               []*ecdsa.PrivateKey
	stopCh                services.StopChan
	subscribers           map[string]*subscriber
	tickerResolution      int
	wg                    sync.WaitGroup
}

type Params struct {
	Logger logger.Logger
}

func New(p Params) (*capability, error) {
	// Needs a start method that starts a loop that sends reports to the registered workflows
	f := 1
	meta := datastreams.Metadata{MinRequiredSignatures: 2*f + 1}
	signers := []*ecdsa.PrivateKey{}
	for i := 0; i < meta.MinRequiredSignatures; i++ {
		// Test keys: need to be the same across nodes
		bytes := make([]byte, 32)
		bytes[31] = uint8(i + 1) // nolint:gosec

		privKey, err := crypto.ToECDSA(bytes)
		if err != nil {
			return nil, err
		}
		signers = append(signers, privKey)

		signerAddr := crypto.PubkeyToAddress(privKey.PublicKey).Bytes()
		meta.Signers = append(meta.Signers, signerAddr)
	}

	return &capability{
		lggr:                  p.Logger,
		meta:                  meta,
		signers:               signers,
		subscribers:           make(map[string]*subscriber),
		tickerResolution:      defaultTickerResolutionMs,
		sendChannelBufferSize: defaultSendChannelBufferSize,
		stopCh:                make(services.StopChan),
	}, nil
}

func (c *capability) Start(ctx context.Context) error {
	c.wg.Add(1)
	go c.loop()
	c.lggr.Info(cronTriggerInfo.ID + " started")
	return nil
}

func (c *capability) Close() error {
	close(c.stopCh)
	c.wg.Wait()
	c.lggr.Info(cronTriggerInfo.ID + " closed")
	return nil
}

func (c *capability) Ready() error {
	return nil
}

func (c *capability) HealthReport() map[string]error {
	return nil
}

func (c *capability) Name() string {
	return cronTriggerInfo.ID
}

func (c *capability) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return cronTriggerInfo, nil
}

func (c *capability) RegisterTrigger(
	ctx context.Context,
	req capabilities.TriggerRegistrationRequest,
) (<-chan capabilities.TriggerResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	config, err := c.ValidateConfig(req.Config)
	if err != nil {
		return nil, err
	}

	if config.MaxFrequencyMs%uint64(c.tickerResolution) != 0 { // nolint:gosec
		return nil, fmt.Errorf("MaxFrequencyMs must be a multiple of %d", c.tickerResolution)
	}

	ch := make(chan capabilities.TriggerResponse, c.sendChannelBufferSize)
	c.subscribers[req.TriggerID] =
		&subscriber{
			Ch:                               ch,
			WorkflowID:                       req.Metadata.WorkflowID,
			Config:                           *config,
			DurationSinceLastTriggerResponse: 0,
		}
	return ch, nil
}

func (c *capability) UnregisterTrigger(ctx context.Context, req capabilities.TriggerRegistrationRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.subscribers, req.TriggerID)
	return nil
}

func (c *capability) ValidateConfig(config *values.Map) (*streamscap.TriggerConfig, error) {
	cfg := &streamscap.TriggerConfig{}
	if err := config.UnwrapTo(cfg); err != nil {
		return nil, err
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func newReport(lggr logger.Logger, feedID [32]byte, price *big.Int, timestamp uint32) ([]byte, error) {
	ctx := context.Background()
	v3Codec := reportcodec.NewReportCodec(feedID, lggr)
	return v3Codec.BuildReport(ctx, v3.ReportFields{
		BenchmarkPrice:     price,
		Timestamp:          timestamp,
		ValidFromTimestamp: timestamp,
		Bid:                price,
		Ask:                price,
		LinkFee:            price,
		NativeFee:          price,
		ExpiresAt:          timestamp + 1000000,
	})
}

func rawReportContext(reportCtx ocrTypes.ReportContext) []byte {
	rc := evmutil.RawReportContext(reportCtx)
	flat := []byte{}
	for _, r := range rc {
		flat = append(flat, r[:]...)
	}
	return flat
}

func (c *capability) loop() {
	defer c.wg.Done()
	ticker := time.NewTicker(time.Duration(c.tickerResolution) * time.Millisecond)
	defer ticker.Stop()

	prices := []int64{1_000, 20_000, 300_000, 4_000_000, 50_000_000}
	ocrEpoch := uint32(0)
	ocrRound := uint8(0)

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			for i := range prices {
				prices[i] = prices[i] + 1
			}

			ocrRound++
			if ocrRound == 10 {
				ocrRound = 0
				ocrEpoch++
			}

			reportCtx := ocrTypes.ReportContext{
				ReportTimestamp: ocrTypes.ReportTimestamp{
					Epoch: ocrEpoch,
					Round: ocrRound,
				},
			}

			if len(c.subscribers) == 0 {
				c.lggr.Debug("No subscribers, skipping")
				continue
			}

			c.processSubscribers(prices, reportCtx)
		}
	}
}

func (c *capability) processSubscribers(prices []int64, reportCtx ocrTypes.ReportContext) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, subscriber := range c.subscribers {
		reports := []datastreams.FeedReport{}
		subscriber.DurationSinceLastTriggerResponse += c.tickerResolution

		if subscriber.DurationSinceLastTriggerResponse < int(subscriber.Config.MaxFrequencyMs) { // nolint:gosec
			continue
		}
		subscriber.DurationSinceLastTriggerResponse = 0

		// Produce and send a TriggerResponse
		timestamp := time.Now().Unix()
		for i, feedID := range subscriber.Config.FeedIds {
			feedID := string(feedID)
			fullReport, err := newReport(
				c.lggr,
				common.HexToHash(feedID),
				big.NewInt(prices[i%5]),
				uint32(timestamp), // nolint:gosec
			)
			if err != nil {
				subscriber.Ch <- capabilities.TriggerResponse{Err: err}
				return
			}

			report := datastreams.FeedReport{
				FeedID:               feedID,
				FullReport:           fullReport,
				ReportContext:        rawReportContext(reportCtx),
				ObservationTimestamp: timestamp,
			}

			// sign report with mock signers
			sigData := append(crypto.Keccak256(report.FullReport), report.ReportContext...)
			hash := crypto.Keccak256(sigData)
			for n := 0; n < c.meta.MinRequiredSignatures; n++ {
				sig, err := crypto.Sign(hash, c.signers[n])
				if err != nil {
					subscriber.Ch <- capabilities.TriggerResponse{Err: err}
					return
				}
				report.Signatures = append(report.Signatures, sig)
			}
			reports = append(reports, report)
		}

		c.lggr.Infow("New set of Mock reports", "timestamp", timestamp, "payload", reports)

		out := datastreams.StreamsTriggerEvent{
			Payload:   reports,
			Metadata:  c.meta,
			Timestamp: timestamp,
		}
		outputsv, err := values.WrapMap(out)
		if err != nil {
			subscriber.Ch <- capabilities.TriggerResponse{Err: err}
			continue
		}
		eventID := fmt.Sprintf("streams_%024s", strconv.FormatInt(timestamp, 10))

		subscriber.Ch <- capabilities.TriggerResponse{
			Event: capabilities.TriggerEvent{
				TriggerType: cronTriggerInfo.ID,
				ID:          eventID,
				Outputs:     outputsv,
			},
		}
	}

	c.lggr.Debugw("Processed subscribers",
		"numSubscribers", len(c.subscribers),
	)
}
