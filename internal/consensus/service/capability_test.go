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
	"github.com/smartcontractkit/capabilities/libs/ocr"
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

// TestNew_OracleFromInjector checks the oracle is made by whatever the run provides, so tests can swap libocr for
// their own.
func TestNew_OracleFromInjector(t *testing.T) {
	var made atomic.Int32
	newOracle := func(args libocr.OracleArgs) (libocr.Oracle, error) {
		made.Add(1)
		ocr3Args, ok := args.(libocr.OCR3OracleArgs2[plugin.ReportInfo])
		assert.True(t, ok, "consensus runs an OCR3 oracle")
		assert.NotNil(t, ocr3Args.ReportingPluginFactory)
		assert.NotNil(t, ocr3Args.BinaryNetworkEndpointFactory, "the node's networking is filled in")
		return noopOracle{}, nil
	}

	capabilitytest.Start(t, New, 1, capability.WithProvider(func(i *capability.Injector) error {
		return i.ProvideValue[ocr.NewOracleFunc](newOracle)
	}))

	assert.Equal(t, int32(1), made.Load())
}

type noopOracle struct{}

func (noopOracle) Start() error { return nil }

func (noopOracle) Close() error { return nil }
