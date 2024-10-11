package testutils

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var _ core.OracleFactory = (*oracleFactory)(nil)

type oracleFactory struct {
	t    *testing.T
	lggr logger.SugaredLogger
}

func NewOracleFactory(t *testing.T, lggr logger.SugaredLogger) *oracleFactory {
	return &oracleFactory{
		t:    t,
		lggr: lggr,
	}
}

// NewOracle(ctx context.Context, args OracleArgs) (Oracle, error)
func (of *oracleFactory) NewOracle(ctx context.Context, args core.OracleArgs) (core.Oracle, error) {
	return &oracle{
		config: args,
		wg:     &sync.WaitGroup{},
		lggr:   of.lggr,
	}, nil
}

type oracle struct {
	config core.OracleArgs
	wg     *sync.WaitGroup
	cancel context.CancelFunc
	lggr   logger.SugaredLogger
}

func (o *oracle) Start(ctx context.Context) error {
	ctx, o.cancel = context.WithCancel(ctx)

	config := ocr3types.ReportingPluginConfig{
		F: 1,
		N: 4,
	}

	reportingPlugin, _, err := o.config.ReportingPluginFactoryService.NewReportingPlugin(
		config,
	)
	if err != nil {
		return err
	}

	outcomeCtx := ocr3types.OutcomeContext{
		SeqNr: 1,
	}
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		for {
			select {
			case <-ctx.Done():
				o.lggr.Info("context canceled, stopping the routine")
				return
			case <-time.After(250 * time.Millisecond):
				// Perform the logic of a reporting plugin
				query, err := reportingPlugin.Query(ctx, outcomeCtx)
				if err != nil {
					o.lggr.Errorf("failed to generate query: %v", err)
					return
				}
				observation, err := reportingPlugin.Observation(
					ctx,
					outcomeCtx,
					query,
				)

				if err != nil {
					o.lggr.Errorf("failed to generate observation: %v", err)
					return
				}

				attributedObservation := ocrtypes.AttributedObservation{
					Observation: observation,
					Observer:    commontypes.OracleID(1),
				}
				if err = reportingPlugin.ValidateObservation(outcomeCtx, query, attributedObservation); err != nil {
					o.lggr.Errorf("failed to validate observation: %v", err)
					return
				}

				// Duplicate the observation N times; happy path expectation.
				// More complex cases can be added later.
				attributedObservations := make([]ocrtypes.AttributedObservation, config.N)
				n := uint8(math.Min(float64(config.N), 4))
				for i := uint8(0); i < n; i++ {
					attributedObservations[i] = ocrtypes.AttributedObservation{
						Observation: observation,
						Observer:    commontypes.OracleID(i),
					}
				}

				newOutcome, err := reportingPlugin.Outcome(outcomeCtx, query, attributedObservations)
				if err != nil {
					o.lggr.Errorf("failed to generate outcome: %v", err)
					return
				}

				// Reports(seqNr uint64, outcome ocr3types.Outcome

				reportsWithInfo, err := reportingPlugin.Reports(outcomeCtx.SeqNr, newOutcome)
				if err != nil {
					o.lggr.Errorf("failed to generate reports: %v", err)
					return
				}

				for _, reportWithInfo := range reportsWithInfo {
					shouldAccept, err := reportingPlugin.ShouldAcceptAttestedReport(
						ctx,
						outcomeCtx.SeqNr,
						reportWithInfo,
					)
					if err != nil {
						o.lggr.Errorf("failed when checking if should accept attested report: %v", err)
						return
					}

					if !shouldAccept {
						continue
					}

					shouldTransmit, err := reportingPlugin.ShouldTransmitAcceptedReport(
						ctx,
						outcomeCtx.SeqNr,
						reportWithInfo,
					)

					if err != nil {
						o.lggr.Errorf("failed when checking if should transmit accepted report: %v", err)
						return
					}

					if !shouldTransmit {
						continue
					}

					err = o.config.ContractTransmitter.Transmit(
						ctx,
						types.ConfigDigest{},
						outcomeCtx.SeqNr,
						reportWithInfo,
						[]types.AttributedOnchainSignature{},
					)
					if err != nil {
						o.lggr.Errorf("failed to transmit report: %v", err)
						return
					}
				}

				// Progress rounds
				outcomeCtx.SeqNr++
				outcomeCtx.PreviousOutcome = newOutcome
			}
		}
	}()

	return nil
}

func (o *oracle) Close(ctx context.Context) error {
	if o.cancel != nil {
		o.cancel()
	}
	o.wg.Wait() // Wait for the goroutine to finish
	return nil
}
