package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jhump/protoreflect/dynamic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
)

const testCapabilityID = "test-capability@1.0.0"

// testService is a real registered proto service with both streaming and
// non-streaming methods, which is what makes it a useful stand-in.
func testService() protoreflect.ServiceDescriptor {
	return pb.File_capabilities_proto.Services().ByName("Executable")
}

func testKey(method string) string {
	return string(testService().FullName()) + "/" + method
}

// fakeCapability stands in for a generated server: it reports an ID and hands back
// the service it would have been generated from.
//
// Executable is used as that service because it is a real registered proto service
// with non-streaming methods, so the descriptor walk and the Go type lookup are
// exercised against generated code rather than something built for the test.
type fakeCapability struct {
	response *anypb.Any
	err      error

	// Guarded: the fan-out calls every instance at once, so Execute is reached
	// concurrently. That is the property under test, not an accident to design
	// around.
	mu          sync.Mutex
	gotRequests []capabilities.CapabilityRequest
}

// requests is what Execute was called with, copied out under the lock.
func (f *fakeCapability) requests() []capabilities.CapabilityRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]capabilities.CapabilityRequest, len(f.gotRequests))
	copy(out, f.gotRequests)
	return out
}

func (f *fakeCapability) Info(context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo(testCapabilityID, capabilities.CapabilityTypeCombined, "a capability for tests")
}

func (f *fakeCapability) Service() protoreflect.ServiceDescriptor {
	return testService()
}

func (f *fakeCapability) Execute(_ context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	f.mu.Lock()
	f.gotRequests = append(f.gotRequests, request)
	err, response := f.err, f.response
	f.mu.Unlock()

	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	return capabilities.CapabilityResponse{Payload: response}, nil
}

func (f *fakeCapability) RegisterToWorkflow(context.Context, capabilities.RegisterToWorkflowRequest) error {
	return nil
}

func (f *fakeCapability) UnregisterFromWorkflow(context.Context, capabilities.UnregisterFromWorkflowRequest) error {
	return nil
}

// fakeRegistry is the whole of what the page asks a registry for, which is the
// point of Registry being one method: resolving an executable by ID.
type fakeRegistry struct {
	capability capabilities.ExecutableCapability
}

func (f fakeRegistry) GetExecutable(_ context.Context, id string) (capabilities.ExecutableCapability, error) {
	if id != testCapabilityID {
		return nil, fmt.Errorf("unknown capability %s", id)
	}
	return f.capability, nil
}

// settle resolves the metadata the way the fan-out handler does: once, so every
// instance is sent the same one.
func settle(t *testing.T, overrides map[string][]string) http.Header {
	t.Helper()
	md, err := MetadataFromHeaders(func(name string) []string { return overrides[name] })
	require.NoError(t, err)
	return HeadersFromMetadata(md)
}

func requireOneRequest(t *testing.T, c *fakeCapability) capabilities.CapabilityRequest {
	t.Helper()
	got := c.requests()
	require.Len(t, got, 1)
	return got[0]
}

func newTestServer(t *testing.T) (*Server, *fakeCapability) {
	t.Helper()

	empty, err := anypb.New(&emptypb.Empty{})
	require.NoError(t, err)
	capability := &fakeCapability{response: empty}

	server, err := New(t.Context(), fakeRegistry{capability: capability}, capability)
	require.NoError(t, err)
	return server, capability
}

// Every non-streaming method is registered, and every streaming one - a trigger -
// is left out, since a trigger is registered rather than called.
func TestNewRegistersOnlyCallableMethods(t *testing.T) {
	server, _ := newTestServer(t)

	service := testService()
	var wantCallable, wantSkipped []string
	for i := range service.Methods().Len() {
		md := service.Methods().Get(i)
		if md.IsStreamingServer() || md.IsStreamingClient() {
			wantSkipped = append(wantSkipped, string(md.Name()))
			continue
		}
		wantCallable = append(wantCallable, string(md.Name()))
	}
	require.NotEmpty(t, wantCallable)
	require.NotEmpty(t, wantSkipped, "Executable has a streaming Execute, which is what proves the skip")

	for _, name := range wantCallable {
		_, ok := server.calls[testKey(name)]
		assert.True(t, ok, "%s should be callable", name)
	}
	for _, name := range wantSkipped {
		_, ok := server.calls[testKey(name)]
		assert.False(t, ok, "%s streams, so it should not be callable", name)
	}

	// The key is the service and method a request arrives with.
	call, ok := server.calls[testKey("RegisterToWorkflow")]
	require.True(t, ok)
	assert.Equal(t, testCapabilityID, call.capabilityID)
	assert.Equal(t, "RegisterToWorkflow", call.method)
	assert.Equal(t, "*pb.RegisterToWorkflowRequest", call.input.String())
	assert.Equal(t, "*emptypb.Empty", call.output.String())
}

// A form submission becomes the CapabilityRequest a host would have sent, and is
// delivered through the registry rather than a connection of its own.
func TestInvokeWrapsTheCallAsACapabilityRequest(t *testing.T) {
	server, capability := newTestServer(t)

	method := "/" + testKey("RegisterToWorkflow")
	call := server.calls[testKey("RegisterToWorkflow")]

	inputDesc, err := server.methodDescriptor("RegisterToWorkflow")
	require.NoError(t, err)

	args := dynamic.NewMessage(inputDesc.GetInputType())
	reply := dynamic.NewMessage(inputDesc.GetOutputType())

	// The metadata reaches Invoke as outgoing gRPC metadata, which is how grpcui
	// forwards the browser's headers.
	suppliedWorkflowID := strings.Repeat("ab", workflowIDBytes)
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.New(map[string]string{
		HeaderPrefix + "WORKFLOW-ID":     suppliedWorkflowID,
		HeaderPrefix + "WORKFLOW-DON-ID": "9",
	}))

	require.NoError(t, server.Invoke(ctx, method, args, reply))

	got := requireOneRequest(t, capability)
	assert.Equal(t, call.method, got.Method)
	assert.Equal(t, testCapabilityID, got.CapabilityId)
	require.NotNil(t, got.Payload)
	require.NotNil(t, got.ConfigPayload, "config is always sent, and always empty")

	// Whatever the page specified, and defaults for the rest.
	assert.Equal(t, suppliedWorkflowID, got.Metadata.WorkflowID)
	assert.EqualValues(t, 9, got.Metadata.WorkflowDonID)
	assert.Equal(t, markedHex(workflowOwnerBytes), got.Metadata.WorkflowOwner)
	assert.False(t, got.Metadata.ExecutionTimestamp.IsZero())
}

func TestInvokeRejectsUnknownMethods(t *testing.T) {
	server, _ := newTestServer(t)
	err := server.Invoke(t.Context(), "/"+testKey("Nope"), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no capability method registered")
}

// Triggers are not offered, so there is nothing to open a stream for.
func TestNewStreamIsRefused(t *testing.T) {
	server, _ := newTestServer(t)
	_, err := server.NewStream(t.Context(), nil, "/"+testKey("Execute"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "triggers are registered rather than called")
}

// Both pages are mounted on the instance's own mux, and the instance is added to
// the fleet so a sibling's fan-out can reach it.
func TestMountServesBothPages(t *testing.T) {
	server, _ := newTestServer(t)

	fleet := &Fleet{}
	mux := http.NewServeMux()
	require.NoError(t, Mount(mux, DefaultPrefix, server, fleet, 0, "test"))

	require.Len(t, fleet.List(), 1)
	assert.Equal(t, 0, fleet.List()[0].Index)

	for path, want := range map[string]int{
		DefaultPrefix + "/ui/":     http.StatusOK,
		DefaultPrefix + "/request": http.StatusOK,
		DefaultPrefix:              http.StatusFound,
		"/debug/capabilities/nope": http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, want, rec.Code, path)
	}
}

// The fan-out reaches an instance by calling into its handler, so a second
// instance in the fleet is callable without a port or a socket.
func TestFanoutInvokesEveryInstanceInProcess(t *testing.T) {
	first, firstCap := newTestServer(t)
	second, secondCap := newTestServer(t)

	fleet := &Fleet{}
	mux := http.NewServeMux()
	require.NoError(t, Mount(mux, DefaultPrefix, first, fleet, 0, "one"))
	// A second mux, as a second instance has, sharing the fleet.
	require.NoError(t, Mount(http.NewServeMux(), DefaultPrefix, second, fleet, 1, "two"))
	require.Len(t, fleet.List(), 2)

	f := &fanout{fleet: fleet, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: first}
	response := f.run(fanoutRequest{
		Method: strings.ReplaceAll(testKey("RegisterToWorkflow"), "/", "."),
		Groups: []requestGroup{{Instances: []int{0, 1}, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
	}, settle(t, map[string][]string{
		HeaderPrefix + "ORG-ID": {"shared-across-instances"},
	}))

	require.Len(t, response.Results, 2)
	for _, row := range response.Results {
		assert.Equal(t, "ok", row.Status, "instance %d: %s", row.Instance, row.Error)
		assert.Equal(t, 1, row.Group)
	}

	// Both instances were called, with the same metadata: that is the point of the
	// page filling it in before sending.
	for _, c := range []*fakeCapability{firstCap, secondCap} {
		assert.Equal(t, "shared-across-instances", requireOneRequest(t, c).Metadata.OrgID)
	}
}

// An instance no group addressed reports N/A rather than being left out.
func TestFanoutReportsUnaddressedInstances(t *testing.T) {
	first, _ := newTestServer(t)
	second, secondCap := newTestServer(t)

	fleet := &Fleet{}
	require.NoError(t, Mount(http.NewServeMux(), DefaultPrefix, first, fleet, 0, "one"))
	require.NoError(t, Mount(http.NewServeMux(), DefaultPrefix, second, fleet, 1, "two"))

	f := &fanout{fleet: fleet, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: first}
	response := f.run(fanoutRequest{
		Method: strings.ReplaceAll(testKey("RegisterToWorkflow"), "/", "."),
		Groups: []requestGroup{{Instances: []int{0}, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
	}, settle(t, nil))

	require.Len(t, response.Results, 2)
	assert.Equal(t, "ok", response.Results[0].Status)
	assert.Equal(t, "na", response.Results[1].Status)
	assert.Empty(t, secondCap.requests(), "an unaddressed instance is not contacted")
}

// quorum is a capability that cannot answer until every instance has been asked,
// which is the shape of an OCR capability: a round needs its participants.
//
// It is what makes the next test meaningful. A serial fan-out leaves the first
// instance waiting for siblings that have not been called yet, so it reports the
// timeout instead of a response.
type quorum struct {
	*fakeCapability

	want     int
	mu       sync.Mutex
	arrived  int
	released chan struct{}
}

func newQuorum(t *testing.T, want int) *quorum {
	t.Helper()
	empty, err := anypb.New(&emptypb.Empty{})
	require.NoError(t, err)
	return &quorum{
		fakeCapability: &fakeCapability{response: empty},
		want:           want,
		released:       make(chan struct{}),
	}
}

func (q *quorum) Execute(ctx context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	q.mu.Lock()
	q.arrived++
	if q.arrived == q.want {
		close(q.released)
	}
	q.mu.Unlock()

	select {
	case <-q.released:
		return q.fakeCapability.Execute(ctx, request)
	case <-time.After(5 * time.Second):
		return capabilities.CapabilityResponse{}, fmt.Errorf("only %d of %d instances were asked: the fan-out is serial", q.arrived, q.want)
	}
}

// Every instance is called at once, so a capability that needs a quorum can form
// one. Serially, the first call would sit waiting for instances that have not been
// invited yet.
func TestFanoutCallsEveryInstanceConcurrently(t *testing.T) {
	const instances = 4

	shared := newQuorum(t, instances)
	fleet := &Fleet{}
	for i := range instances {
		server, err := New(t.Context(), fakeRegistry{capability: shared}, shared)
		require.NoError(t, err)
		require.NoError(t, Mount(http.NewServeMux(), DefaultPrefix, server, fleet, i, fmt.Sprintf("instance %d", i+1)))
	}
	require.Len(t, fleet.List(), instances)

	first, err := New(t.Context(), fakeRegistry{capability: shared}, shared)
	require.NoError(t, err)
	f := &fanout{fleet: fleet, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: first}

	targets := make([]int, 0, instances)
	for i := range instances {
		targets = append(targets, i)
	}

	done := make(chan fanoutResponse, 1)
	go func() {
		done <- f.run(fanoutRequest{
			Method: strings.ReplaceAll(testKey("RegisterToWorkflow"), "/", "."),
			Groups: []requestGroup{{Instances: targets, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
		}, settle(t, nil))
	}()

	var response fanoutResponse
	select {
	case response = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the fan-out never returned")
	}

	require.Len(t, response.Results, instances)
	for _, row := range response.Results {
		assert.Equal(t, "ok", row.Status, "instance %d: %s", row.Instance, row.Error)
	}
	assert.Len(t, shared.requests(), instances, "every instance should have been asked")
}

// One fan-out is one request, so every instance must be asked under the same
// execution - otherwise a capability keying work on it sees four executions where
// the user made one. Two fan-outs are two requests, so those must differ.
func TestFanoutSendsOneMetadataToEveryInstance(t *testing.T) {
	first, firstCap := newTestServer(t)
	second, secondCap := newTestServer(t)

	fleet := &Fleet{}
	require.NoError(t, Mount(http.NewServeMux(), DefaultPrefix, first, fleet, 0, "one"))
	require.NoError(t, Mount(http.NewServeMux(), DefaultPrefix, second, fleet, 1, "two"))

	f := &fanout{fleet: fleet, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: first}
	request := func() fanoutRequest {
		return fanoutRequest{
			Method: strings.ReplaceAll(testKey("RegisterToWorkflow"), "/", "."),
			Groups: []requestGroup{{Instances: []int{0, 1}, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
		}
	}

	// Nothing specified, so every field is a default - which is the case that
	// would otherwise let each instance invent its own.
	f.run(request(), settle(t, nil))

	firstExecution := requireOneRequest(t, firstCap).Metadata.WorkflowExecutionID
	secondExecution := requireOneRequest(t, secondCap).Metadata.WorkflowExecutionID
	require.NotEmpty(t, firstExecution)
	assert.Equal(t, firstExecution, secondExecution, "one fan-out is one execution")

	// A second fan-out is a second execution.
	f.run(request(), settle(t, nil))

	later := firstCap.requests()
	require.Len(t, later, 2)
	assert.NotEqual(t, later[0].Metadata.WorkflowExecutionID, later[1].Metadata.WorkflowExecutionID,
		"a new fan-out is a new execution")
}

// The fan-out endpoint answers a value it cannot parse with a 400, and keeps 500
// for the page itself failing.
func TestFanoutEndpointSeparatesUserErrorsFromSystemErrors(t *testing.T) {
	server, _ := newTestServer(t)
	fleet := &Fleet{}
	mux := http.NewServeMux()
	require.NoError(t, Mount(mux, DefaultPrefix, server, fleet, 0, "one"))

	post := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, DefaultPrefix+"/request/fanout", strings.NewReader(body))
		req.Header.Set(fanoutHeaderName, "token")
		req.AddCookie(&http.Cookie{Name: fanoutCookieName, Value: "token"})

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	t.Run("a value that will not parse", func(t *testing.T) {
		rec := post(t, `{"method":"a.B.C","groups":[],"metadata":{"`+
			HeaderPrefix+`WORKFLOW-DON-ID":["not-a-number"]}}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "WORKFLOW-DON-ID")
		assert.NotContains(t, rec.Body.String(), "rpc error", "the transport should not be in the message")
	})

	t.Run("a body that is not JSON", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, post(t, `not json`).Code)
	})

	t.Run("no method", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, post(t, `{"groups":[]}`).Code)
	})

	t.Run("no CSRF token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, DefaultPrefix+"/request/fanout", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

// Invoke's failures are classified, because that is what decides whether grpcui
// reports a mistyped field as a broken server or as a failed call.
func TestInvokeClassifiesFailures(t *testing.T) {
	server, _ := newTestServer(t)

	t.Run("a method that is not offered is the caller's", func(t *testing.T) {
		err := server.Invoke(t.Context(), "/"+testKey("Nope"), nil, nil)
		require.Error(t, err)
		assert.True(t, isUserError(err), "%v", err)
		assert.Equal(t, http.StatusBadRequest, httpStatus(err))
	})

	t.Run("streaming is not offered", func(t *testing.T) {
		_, err := server.NewStream(t.Context(), nil, "/"+testKey("Execute"))
		require.Error(t, err)
		assert.True(t, isUserError(err), "%v", err)
	})

	t.Run("a request of the wrong shape is ours", func(t *testing.T) {
		// Not a dynamic message and not the method's own type: the page cannot
		// produce this, so it is a fault rather than a mistake.
		err := server.Invoke(t.Context(), "/"+testKey("RegisterToWorkflow"), "not a message", nil)
		require.Error(t, err)
		assert.False(t, isUserError(err), "%v", err)
		assert.Equal(t, http.StatusInternalServerError, httpStatus(err))
	})
}

// A capability that fails is not the page failing, and a capability that says the
// fault was the user's is repeated as such rather than flattened into a 500.
func TestCapabilityErrorsAreClassifiedByOrigin(t *testing.T) {
	for name, tc := range map[string]struct {
		err        error
		userError  bool
		httpStatus int
	}{
		"the capability blames the user": {
			err:        caperrors.NewPublicUserError(fmt.Errorf("your input is wrong"), caperrors.InvalidArgument),
			userError:  true,
			httpStatus: http.StatusBadRequest,
		},
		"the capability blames itself": {
			err:        caperrors.NewPublicSystemError(fmt.Errorf("i am broken"), caperrors.Internal),
			userError:  false,
			httpStatus: http.StatusInternalServerError,
		},
		"a plain error carries no origin": {
			err:        fmt.Errorf("something happened"),
			userError:  false,
			httpStatus: http.StatusInternalServerError,
		},
	} {
		t.Run(name, func(t *testing.T) {
			classified := fromCapability(tc.err)
			require.Error(t, classified)
			assert.Equal(t, tc.userError, isUserError(classified), "%v", classified)
			assert.Equal(t, tc.httpStatus, httpStatus(classified))

			// Whatever the origin, it comes back as a gRPC status so grpcui renders
			// it as a failed call rather than answering 500 "Unexpected error".
			_, ok := status.FromError(classified)
			assert.True(t, ok, "a capability failure should reach the page as a status")
		})
	}

	assert.NoError(t, fromCapability(nil))
}
