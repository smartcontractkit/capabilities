package ocr

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3shims"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/capability/ocr/remote"
)

// TestRemoteOraclesReachConsensus serves each instance's in-process networking the way a node serves its peer, so the
// oracles run over the network client.
func TestRemoteOraclesReachConsensus(t *testing.T) {
	t.Parallel()
	inproc := newNetwork()
	testOraclesReachConsensus(t, func(id int) ocrtypes.BinaryNetworkEndpointFactory {
		network, err := embeddedNetwork(inproc, id)
		require.NoError(t, err)

		lis := bufconn.Listen(1024 * 1024)
		s := grpc.NewServer(grpc.MaxRecvMsgSize(remote.MaxMessageSize))
		require.NoError(t, remote.RegisterServer(s, logger.Test(t), func(context.Context) (ocrtypes.BinaryNetworkEndpointFactory, error) {
			return network.BinaryNetworkEndpointFactory, nil
		}))
		go func() { _ = s.Serve(lis) }()
		t.Cleanup(s.Stop)

		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return remote.NewClient(logger.Test(t), conn, network.BinaryNetworkEndpointFactory.PeerID())
	})
}

// testOraclesReachConsensus runs a DON of testInstances oracles, each over the networking newFactory returns, and
// checks every one transmits an attested report.
func testOraclesReachConsensus(t *testing.T, newFactory func(id int) ocrtypes.BinaryNetworkEndpointFactory) {
	config := testConfig(t)
	transmitted := make(chan transmission, 100)
	for id := range testInstances {
		lggr := logger.Test(t)
		k := testKeysOf(t, id)
		oracle, err := libocr.NewOracle(libocr.OCR3OracleArgs2[string]{
			BinaryNetworkEndpointFactory: newFactory(id),
			ContractConfigTracker:        staticConfig{config: config},
			OffchainConfigDigester:       staticConfig{config: config},
			OffchainKeyring:              k,
			OnchainKeyring:               ocr3shims.OnchainKeyringAsOnchainKeyring2[string](k),
			LocalConfig:                  testLocalConfig(),
			ReportingPluginFactory:       echoPluginFactory{},
			ContractTransmitter:          &recordingTransmitter{id: id, account: k.transmitAccount(), transmitted: transmitted},
			Database:                     NewMemoryDB(fmt.Sprint(id), lggr),
			Logger:                       logger.NewOCRWrapper(lggr, true, func(string) {}),
			MetricsRegisterer:            prometheus.NewRegistry(),
		})
		require.NoError(t, err)
		require.NoError(t, oracle.Start())
		t.Cleanup(func() { assert.NoError(t, oracle.Close()) })
	}

	seen := map[int]bool{}
	timeout := time.After(30 * time.Second)
	for len(seen) < testInstances {
		select {
		case tr := <-transmitted:
			assert.Equal(t, []byte("report"), tr.report)
			assert.GreaterOrEqual(t, tr.signatures, 2, "a report is attested by more than f oracles")
			seen[tr.id] = true
		case <-timeout:
			require.FailNow(t, "timed out waiting for every instance to transmit", "transmitted: %v", seen)
		}
	}
}
