package capability

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// TestLimitsFactoryIsBuiltOverTheSettings covers the wiring the whole thing exists for: a limit made
// by this factory reads the settings this process holds, so an update reaches it.
func TestLimitsFactoryIsBuiltOverTheSettings(t *testing.T) {
	s := newSettings(logger.Test(t))

	f := newLimitsFactory(logger.Test(t), s)

	assert.Same(t, s, f.Settings, "the factory should poll the settings this process holds")
	assert.NotNil(t, f.Meter)
	assert.NotNil(t, f.Logger)
}

// TestFollowHostSettingsAppliesUpdates covers the host pushing new settings: a limit made from this
// process's factory resolves against the new payload on its next use, without anything having to
// be told.
func TestFollowHostSettingsAppliesUpdates(t *testing.T) {
	s := newSettings(logger.Test(t))

	// A global-scope setting, so no tenant is needed to resolve it. The compiled-in default is 0s.
	limit, err := newLimitsFactory(logger.Test(t), s).MakeTimeLimiter(cresettings.Default.TriggerRegistrationStatusUpdateTimeout)
	require.NoError(t, err)

	host := newFakeBroadcaster()
	go followHostSettings(t.Context(), logger.Test(t), host, s)

	host.send(t, core.SettingsUpdate{Settings: "[global]\nTriggerRegistrationStatusUpdateTimeout = \"15s\"\n", Hash: "a"})

	require.Eventually(t, func() bool {
		got, err := limit.Limit(t.Context())
		return err == nil && got == 15*time.Second
	}, 5*time.Second, 5*time.Millisecond, "the limit should resolve against the pushed settings")
}

// TestFollowHostSettingsKeepsThePreviousSettingsOnFailure covers a payload that cannot be applied:
// what was in force stays in force, and later updates still land.
func TestFollowHostSettingsKeepsThePreviousSettingsOnFailure(t *testing.T) {
	s := newSettings(logger.Test(t))
	limit, err := newLimitsFactory(logger.Test(t), s).MakeTimeLimiter(cresettings.Default.TriggerRegistrationStatusUpdateTimeout)
	require.NoError(t, err)

	host := newFakeBroadcaster()
	go followHostSettings(t.Context(), logger.Test(t), host, s)

	host.send(t, core.SettingsUpdate{Settings: "[global]\nTriggerRegistrationStatusUpdateTimeout = \"15s\"\n", Hash: "a"})
	host.send(t, core.SettingsUpdate{Settings: "not a settings payload", Hash: "b"})
	host.send(t, core.SettingsUpdate{Settings: "[global]\nTriggerRegistrationStatusUpdateTimeout = \"20s\"\n", Hash: "c"})

	require.Eventually(t, func() bool {
		got, err := limit.Limit(t.Context())
		return err == nil && got == 20*time.Second
	}, 5*time.Second, 5*time.Millisecond, "updates after a bad one should still be applied")
}

// TestFollowHostSettingsResubscribes covers the host being unreachable or its stream ending: the
// subscription is retried rather than abandoned.
func TestFollowHostSettingsResubscribes(t *testing.T) {
	s := newSettings(logger.Test(t))
	host := newFakeBroadcaster()
	host.failFirst.Store(true)

	go followHostSettings(t.Context(), logger.Test(t), host, s)

	host.send(t, core.SettingsUpdate{Settings: "[global]\n", Hash: "a"})
	host.closeStream()
	host.send(t, core.SettingsUpdate{Settings: "[global]\n", Hash: "b"})

	assert.GreaterOrEqual(t, host.subscriptions.Load(), int32(3))
}

// fakeBroadcaster hands out one stream per subscription, and sends on whichever is current.
type fakeBroadcaster struct {
	failFirst     atomic.Bool
	subscriptions atomic.Int32
	streams       chan chan core.SettingsUpdate
	current       chan core.SettingsUpdate
}

func newFakeBroadcaster() *fakeBroadcaster {
	return &fakeBroadcaster{streams: make(chan chan core.SettingsUpdate, 1)}
}

func (f *fakeBroadcaster) Subscribe(context.Context) (<-chan core.SettingsUpdate, error) {
	f.subscriptions.Add(1)
	if f.failFirst.CompareAndSwap(true, false) {
		return nil, errors.New("host unavailable")
	}
	ch := make(chan core.SettingsUpdate)
	f.streams <- ch
	return ch, nil
}

func (f *fakeBroadcaster) send(t *testing.T, u core.SettingsUpdate) {
	t.Helper()
	if f.current == nil {
		select {
		case f.current = <-f.streams:
		case <-time.After(5 * time.Second):
			t.Fatal("nothing subscribed to the host's settings")
		}
	}
	select {
	case f.current <- u:
	case <-time.After(5 * time.Second):
		t.Fatal("the settings update was not received")
	}
}

func (f *fakeBroadcaster) closeStream() {
	close(f.current)
	f.current = nil
}

// TestSettingsServiceWithoutAURL covers a run the host pushes no settings to: it starts and stops
// cleanly, and every limit stays at its compiled-in default.
func TestSettingsServiceWithoutAURL(t *testing.T) {
	s, err := startSettings(t.Context(), logger.Test(t), settingsConfig{})
	require.NoError(t, err)
	assert.Nil(t, s.conn)
	require.NoError(t, s.Close())
}

// TestSettingsServiceFollowsTheHost covers --settings.url end to end: the host's settings service
// pushes an update over gRPC, and a limit made from this process's factory resolves against it.
func TestSettingsServiceFollowsTheHost(t *testing.T) {
	host := &settingsServer{updates: make(chan *pb.SettingsUpdate, 1)}
	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	pb.RegisterSettingsServer(srv, host)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(srv.Stop)

	s, err := startSettings(t.Context(), logger.Test(t), settingsConfig{URL: l.Addr().String()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	limit, err := newLimitsFactory(logger.Test(t), s.values).MakeTimeLimiter(cresettings.Default.TriggerRegistrationStatusUpdateTimeout)
	require.NoError(t, err)

	host.updates <- &pb.SettingsUpdate{Settings: "[global]\nTriggerRegistrationStatusUpdateTimeout = \"15s\"\n", Hash: "a"}

	require.Eventually(t, func() bool {
		got, err := limit.Limit(t.Context())
		return err == nil && got == 15*time.Second
	}, 10*time.Second, 5*time.Millisecond, "the limit should resolve against the host's settings")
}

type settingsServer struct {
	pb.UnimplementedSettingsServer
	updates chan *pb.SettingsUpdate
}

func (s *settingsServer) Subscribe(_ *emptypb.Empty, stream grpc.ServerStreamingServer[pb.SettingsUpdate]) error {
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case u := <-s.updates:
			if err := stream.Send(u); err != nil {
				return err
			}
		}
	}
}
