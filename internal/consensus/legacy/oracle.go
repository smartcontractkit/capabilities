package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/plugin"
)

// oracleTimeout is how long the node has to make or stop the oracle.
const oracleTimeout = time.Minute

const (
	// keyBundleNameField is where the node's multi-chain keyring reads the key bundle to sign a report with.
	keyBundleNameField = "keyBundleName"
	infoField          = "info"
)

// newOracle makes the consensus oracle with the node's oracle factory. The node runs it over report infos that are
// bytes, so each report's info is encoded with its key bundle where the node's keyring finds it, and decoded again
// before it reaches the plugin or the transmitter.
func newOracle(factory core.OracleFactory) func(libocr.OCR3OracleArgs2[plugin.ReportInfo]) (libocr.Oracle, error) {
	return func(args libocr.OCR3OracleArgs2[plugin.ReportInfo]) (libocr.Oracle, error) {
		ctx, cancel := context.WithTimeout(context.Background(), oracleTimeout)
		defer cancel()

		o, err := factory.NewOracle(ctx, core.OracleArgs{
			LocalConfig:                   args.LocalConfig,
			ReportingPluginFactoryService: pluginFactory{factory: args.ReportingPluginFactory},
			ContractTransmitter:           transmitter{transmitter: args.ContractTransmitter},
		})
		if err != nil {
			return nil, err
		}

		return oracle{oracle: o}, nil
	}
}

// oracle is the node's oracle as libocr's.
type oracle struct {
	oracle core.Oracle
}

func (o oracle) Start() error {
	ctx, cancel := context.WithTimeout(context.Background(), oracleTimeout)
	defer cancel()
	return o.oracle.Start(ctx)
}

func (o oracle) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), oracleTimeout)
	defer cancel()
	return o.oracle.Close(ctx)
}

type pluginFactory struct {
	factory ocr3types.ReportingPluginFactory[plugin.ReportInfo]
}

func (f pluginFactory) NewReportingPlugin(ctx context.Context, cfg ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[[]byte], ocr3types.ReportingPluginInfo, error) {
	p, info, err := f.factory.NewReportingPlugin(ctx, cfg)
	if err != nil {
		return nil, info, err
	}

	return reportingPlugin{ReportingPlugin: p}, info, nil
}

// reportingPlugin is the plugin with its report infos encoded. Only the methods that take or return them differ.
type reportingPlugin struct {
	ocr3types.ReportingPlugin[plugin.ReportInfo]
}

func (p reportingPlugin) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	reports, err := p.ReportingPlugin.Reports(ctx, seqNr, outcome)
	if err != nil {
		return nil, err
	}

	encoded := make([]ocr3types.ReportPlus[[]byte], len(reports))
	for i, r := range reports {
		info, err := encodeInfo(r.ReportWithInfo.Info)
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

func (p reportingPlugin) ShouldAcceptAttestedReport(ctx context.Context, seqNr uint64, r ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	decoded, err := decodeReport(r)
	if err != nil {
		return false, err
	}

	return p.ReportingPlugin.ShouldAcceptAttestedReport(ctx, seqNr, decoded)
}

func (p reportingPlugin) ShouldTransmitAcceptedReport(ctx context.Context, seqNr uint64, r ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	decoded, err := decodeReport(r)
	if err != nil {
		return false, err
	}

	return p.ReportingPlugin.ShouldTransmitAcceptedReport(ctx, seqNr, decoded)
}

type transmitter struct {
	transmitter ocr3types.ContractTransmitter[plugin.ReportInfo]
}

func (t transmitter) Transmit(ctx context.Context, digest ocrtypes.ConfigDigest, seqNr uint64, r ocr3types.ReportWithInfo[[]byte],
	sigs []ocrtypes.AttributedOnchainSignature) error {
	decoded, err := decodeReport(r)
	if err != nil {
		return err
	}

	return t.transmitter.Transmit(ctx, digest, seqNr, decoded, sigs)
}

func (t transmitter) FromAccount(ctx context.Context) (ocrtypes.Account, error) {
	return t.transmitter.FromAccount(ctx)
}

func encodeInfo(info plugin.ReportInfo) ([]byte, error) {
	value, err := json.Marshal(info.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to encode report info: %w", err)
	}

	s, err := structpb.NewStruct(map[string]any{keyBundleNameField: info.KeyBundle, infoField: string(value)})
	if err != nil {
		return nil, fmt.Errorf("failed to encode report info: %w", err)
	}

	return proto.MarshalOptions{Deterministic: true}.Marshal(s)
}

func decodeReport(r ocr3types.ReportWithInfo[[]byte]) (ocr3types.ReportWithInfo[plugin.ReportInfo], error) {
	s := &structpb.Struct{}
	if err := proto.Unmarshal(r.Info, s); err != nil {
		return ocr3types.ReportWithInfo[plugin.ReportInfo]{}, fmt.Errorf("failed to decode report info: %w", err)
	}

	info := plugin.ReportInfo{KeyBundle: s.GetFields()[keyBundleNameField].GetStringValue()}
	if err := json.Unmarshal([]byte(s.GetFields()[infoField].GetStringValue()), &info.Value); err != nil {
		return ocr3types.ReportWithInfo[plugin.ReportInfo]{}, fmt.Errorf("failed to decode report info: %w", err)
	}

	return ocr3types.ReportWithInfo[plugin.ReportInfo]{Report: r.Report, Info: info}, nil
}
