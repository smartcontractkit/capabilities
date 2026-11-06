// Package legacyocr makes a capability's oracles with the node's oracle factory, for a capability still served as a
// StandardCapabilities LOOP. It is apart from libs/ocr so that what only legacy needs stays out of it.
package legacyocr

import (
	"context"
	"encoding/json"
	"fmt"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/libs/ocr"
	"github.com/smartcontractkit/capabilities/libs/ocr/internal/nodeinfo"
)

// NewOracle returns libocr.NewOracle for oracles the node makes with factory. args must be
// libocr.OCR3OracleArgs2[ocr.ReportInfo[T]], and only its local config, reporting plugin factory and contract
// transmitter are used: the node fills in the rest. The node's oracles report []byte info, so each report's info is
// encoded for it, and decoded again for the transmitter. Value is encoded as JSON, which nothing outside this process
// reads.
func NewOracle[T any](factory core.OracleFactory) func(libocr.OracleArgs) (libocr.Oracle, error) {
	return func(oracleArgs libocr.OracleArgs) (libocr.Oracle, error) {
		args, ok := oracleArgs.(libocr.OCR3OracleArgs2[ocr.ReportInfo[T]])
		if !ok {
			return nil, fmt.Errorf("the node makes oracles of %T, not %T", libocr.OCR3OracleArgs2[ocr.ReportInfo[T]]{}, oracleArgs)
		}

		oracle, err := factory.NewOracle(context.Background(), core.OracleArgs{
			LocalConfig:                   args.LocalConfig,
			ReportingPluginFactoryService: pluginFactory[T]{factory: args.ReportingPluginFactory},
			ContractTransmitter:           transmitter[T]{transmitter: args.ContractTransmitter},
		})
		if err != nil {
			return nil, err
		}

		return nodeOracle{oracle: oracle}, nil
	}
}

// nodeOracle is the node's oracle as libocr's. libocr's has no context, and the node's uses it only to call over the
// LOOP, so it gets one that is never done.
type nodeOracle struct {
	oracle core.Oracle
}

func (o nodeOracle) Start() error { return o.oracle.Start(context.Background()) }

func (o nodeOracle) Close() error { return o.oracle.Close(context.Background()) }

func encode[T any](info ocr.ReportInfo[T]) ([]byte, error) {
	v, err := json.Marshal(info.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to encode report info: %w", err)
	}

	return nodeinfo.Encode(info.KeyBundle, v)
}

func decode[T any](b []byte) (ocr.ReportInfo[T], error) {
	keyBundle, v, err := nodeinfo.Decode(b)
	if err != nil {
		return ocr.ReportInfo[T]{}, err
	}

	info := ocr.ReportInfo[T]{KeyBundle: keyBundle}
	if err = json.Unmarshal(v, &info.Value); err != nil {
		return ocr.ReportInfo[T]{}, fmt.Errorf("failed to decode report info: %w", err)
	}

	return info, nil
}

type pluginFactory[T any] struct {
	factory ocr3types.ReportingPluginFactory[ocr.ReportInfo[T]]
}

func (f pluginFactory[T]) NewReportingPlugin(ctx context.Context, config ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[[]byte], ocr3types.ReportingPluginInfo, error) {
	p, info, err := f.factory.NewReportingPlugin(ctx, config)
	if err != nil {
		return nil, info, err
	}

	return plugin[T]{ReportingPlugin: p}, info, nil
}

// plugin is a plugin of []byte info, encoding and decoding its reports' info.
type plugin[T any] struct {
	ocr3types.ReportingPlugin[ocr.ReportInfo[T]]
}

func (p plugin[T]) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	reports, err := p.ReportingPlugin.Reports(ctx, seqNr, outcome)
	if err != nil {
		return nil, err
	}

	encoded := make([]ocr3types.ReportPlus[[]byte], len(reports))
	for i, r := range reports {
		info, err := encode(r.ReportWithInfo.Info)
		if err != nil {
			return nil, err
		}

		encoded[i] = ocr3types.ReportPlus[[]byte]{
			ReportWithInfo:               ocr3types.ReportWithInfo[[]byte]{Report: r.ReportWithInfo.Report, Info: info},
			TransmissionScheduleOverride: r.TransmissionScheduleOverride,
		}
	}

	return encoded, nil
}

func (p plugin[T]) ShouldAcceptAttestedReport(ctx context.Context, seqNr uint64, report ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	r, err := decodeReport[T](report)
	if err != nil {
		return false, err
	}

	return p.ReportingPlugin.ShouldAcceptAttestedReport(ctx, seqNr, r)
}

func (p plugin[T]) ShouldTransmitAcceptedReport(ctx context.Context, seqNr uint64, report ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	r, err := decodeReport[T](report)
	if err != nil {
		return false, err
	}

	return p.ReportingPlugin.ShouldTransmitAcceptedReport(ctx, seqNr, r)
}

// transmitter transmits reports of []byte info, decoding their info.
type transmitter[T any] struct {
	transmitter ocr3types.ContractTransmitter[ocr.ReportInfo[T]]
}

func (t transmitter[T]) Transmit(ctx context.Context, digest ocrtypes.ConfigDigest, seqNr uint64,
	report ocr3types.ReportWithInfo[[]byte], signatures []ocrtypes.AttributedOnchainSignature) error {
	r, err := decodeReport[T](report)
	if err != nil {
		return err
	}

	return t.transmitter.Transmit(ctx, digest, seqNr, r, signatures)
}

func (t transmitter[T]) FromAccount(ctx context.Context) (ocrtypes.Account, error) {
	return t.transmitter.FromAccount(ctx)
}

func decodeReport[T any](report ocr3types.ReportWithInfo[[]byte]) (ocr3types.ReportWithInfo[ocr.ReportInfo[T]], error) {
	info, err := decode[T](report.Info)
	if err != nil {
		return ocr3types.ReportWithInfo[ocr.ReportInfo[T]]{}, err
	}

	return ocr3types.ReportWithInfo[ocr.ReportInfo[T]]{Report: report.Report, Info: info}, nil
}
