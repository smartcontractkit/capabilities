package service

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"

	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/plugin"
	"github.com/smartcontractkit/capabilities/internal/consensus/protos"
	"github.com/smartcontractkit/capabilities/internal/consensus/protos/protostest"
	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/capabilitytest"
)

// TestNew runs a DON of embedded instances, each with its own oracle, and checks they agree on a median.
func TestNew(t *testing.T) {
	caps := capabilitytest.Start(t, New, 4)

	info, err := caps[0].Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, protos.ConsensusID, info.ID)

	result, err := protostest.ConsensusInstances(caps).Simple(t.Context(), newRequestMetaData(), median(values.Proto(values.NewInt64(10))))
	require.NoError(t, err)
	assert.True(t, proto.Equal(values.Proto(values.NewInt64(10)), result.Response), "got %v", result.Response)
}

// TestNew_OracleFromInjector checks the oracle is made by whatever the run provides, so tests can swap it.
func TestNew_OracleFromInjector(t *testing.T) {
	var made atomic.Int32
	newOracle := func(args libocr.OCR3OracleArgs2[plugin.ReportInfo]) (libocr.Oracle, error) {
		made.Add(1)
		assert.NotNil(t, args.ReportingPluginFactory)
		return noopOracle{}, nil
	}

	capabilitytest.Start(t, New, 1, capability.WithProvider(func(i *capability.Injector) error {
		return i.ProvideValue(newOracle)
	}))

	assert.Equal(t, int32(1), made.Load())
}

type noopOracle struct{}

func (noopOracle) Start() error { return nil }

func (noopOracle) Close() error { return nil }
