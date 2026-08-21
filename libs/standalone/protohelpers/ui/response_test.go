package ui

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
)

// The method the fan-out calls here is BaseCapability.Info, because comparing
// what instances answered needs a response with a field to differ in - and every
// unary method of Executable, which the rest of these tests use, returns Empty.
func infoService() protoreflect.ServiceDescriptor {
	return pb.File_capabilities_proto.Services().ByName("BaseCapability")
}

func infoKey() string {
	return string(infoService().FullName()) + "/Info"
}

// answering builds one mounted instance whose capability answers with the given
// payload.
func answering(t *testing.T, fleet *Fleet, index int, answer string) *Server {
	t.Helper()

	payload, err := anypb.New(&pb.CapabilityInfoReply{
		Id:          testCapabilityID,
		Description: answer,
	})
	require.NoError(t, err)

	capability := &fakeCapability{response: payload, service: infoService()}
	server, err := New(t.Context(), fakeRegistry{capability: capability}, capability)
	require.NoError(t, err)
	require.NoError(t, mount(http.NewServeMux(), server, fleet, nil, index, fmt.Sprintf("instance %d", index+1)))
	return server
}

// fanoutOver runs one request across every instance of the fleet.
func fanoutOver(t *testing.T, fleet *Fleet, server *Server) fanoutResponse {
	t.Helper()

	f := &fanout{fleet: fleet, hub: NewHub(), prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: server}

	targets := make([]int, 0, len(fleet.List()))
	for _, in := range fleet.List() {
		targets = append(targets, in.Index)
	}

	return f.run(fanoutRequest{
		Method: strings.ReplaceAll(infoKey(), "/", "."),
		Groups: []requestGroup{{Instances: targets, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
	}, settle(t, nil))
}

// Instances that answered the same thing are one response, not one response each.
//
// This is what keeps the page readable: four instances agreeing used to be four
// copies of the same JSON, with nothing saying they matched.
func TestFanoutHoldsEachDistinctResponseOnce(t *testing.T) {
	const instances = 4

	fleet := &Fleet{}
	var first *Server
	for i := range instances {
		server := answering(t, fleet, i, "the same answer")
		if i == 0 {
			first = server
		}
	}

	response := fanoutOver(t, fleet, first)
	require.Len(t, response.Results, instances)

	// One answer, held once.
	require.Len(t, response.Payloads, 1)
	require.Len(t, response.PayloadIDs, 1)
	assert.False(t, response.Diverged)

	// And every instance points at it.
	for _, row := range response.Results {
		assert.Equal(t, "ok", row.Status, "instance %d: %s", row.Instance, row.Error)
		assert.Equal(t, 0, row.ResponseIndex, "instance %d", row.Instance)
		assert.Equal(t, response.PayloadIDs[0], row.ResponseID, "instance %d", row.Instance)
	}
}

// Instances that answered differently are as many responses as there were
// answers, and the fan-out says they disagreed.
func TestFanoutReportsDisagreeingInstances(t *testing.T) {
	fleet := &Fleet{}
	first := answering(t, fleet, 0, "one answer")
	answering(t, fleet, 1, "a different answer")
	answering(t, fleet, 2, "one answer")

	response := fanoutOver(t, fleet, first)
	require.Len(t, response.Results, 3)

	require.Len(t, response.Payloads, 2, "two distinct answers between three instances")
	require.Len(t, response.PayloadIDs, 2)
	assert.True(t, response.Diverged)

	// Instances 1 and 3 agreed with each other, instance 2 did not.
	assert.Equal(t, 0, response.Results[0].ResponseIndex)
	assert.Equal(t, 1, response.Results[1].ResponseIndex)
	assert.Equal(t, 0, response.Results[2].ResponseIndex)

	// Every hash describes the response at its own index.
	for _, row := range response.Results {
		assert.Equal(t, response.PayloadIDs[row.ResponseIndex], row.ResponseID, "instance %d", row.Instance)
	}
}

// The numbering is the same for two identical fan-outs.
//
// Instances answer concurrently, so arrival order is arbitrary; numbering by it
// would renumber the columns between two identical runs, which reads as the page
// having found something.
func TestResponseNumberingIsStableAcrossRuns(t *testing.T) {
	fleet := &Fleet{}
	first := answering(t, fleet, 0, "from one")
	answering(t, fleet, 1, "from two")
	answering(t, fleet, 2, "from three")

	firstRun := fanoutOver(t, fleet, first)
	for range 5 {
		again := fanoutOver(t, fleet, first)
		assert.Equal(t, firstRun.PayloadIDs, again.PayloadIDs)
		for i, row := range again.Results {
			assert.Equal(t, firstRun.Results[i].ResponseIndex, row.ResponseIndex,
				"instance %d was numbered differently on a second identical run", row.Instance)
		}
	}
}

// An instance that was not addressed, or that failed, points at no response -
// pointing at the first one would read as having agreed with it.
func TestUnansweredInstancesPointAtNoResponse(t *testing.T) {
	fleet := &Fleet{}
	first := answering(t, fleet, 0, "an answer")
	answering(t, fleet, 1, "an answer")

	f := &fanout{fleet: fleet, hub: NewHub(), prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: first}

	// Only the first instance is addressed.
	response := f.run(fanoutRequest{
		Method: strings.ReplaceAll(infoKey(), "/", "."),
		Groups: []requestGroup{{Instances: []int{0}, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
	}, settle(t, nil))

	require.Len(t, response.Results, 2)
	assert.Equal(t, "ok", response.Results[0].Status)
	assert.Equal(t, 0, response.Results[0].ResponseIndex)

	assert.Equal(t, "na", response.Results[1].Status)
	assert.Equal(t, -1, response.Results[1].ResponseIndex)
	assert.Empty(t, response.Results[1].ResponseID)

	// One instance answering is not disagreement.
	assert.False(t, response.Diverged)
	assert.Len(t, response.Payloads, 1)
}

// The response is not repeated per instance on the wire either, which is the
// point: it is held once and pointed at.
func TestTheResponseIsNotRepeatedPerInstance(t *testing.T) {
	const instances = 4

	fleet := &Fleet{}
	var first *Server
	for i := range instances {
		server := answering(t, fleet, i, "a long answer worth not repeating four times over")
		if i == 0 {
			first = server
		}
	}

	response := fanoutOver(t, fleet, first)
	require.Len(t, response.Payloads, 1)

	for _, row := range response.Results {
		// The struct has nowhere to put one, which is the guarantee.
		assert.NotContains(t, fmt.Sprintf("%+v", row), "worth not repeating",
			"instance %d carried the response as well as pointing at it", row.Instance)
	}
}

// A method's own response type is unaffected by any of this: the dedupe is over
// what the instances answered, not over what the method returns.
func TestDedupeDoesNotTouchTheMethodItself(t *testing.T) {
	fleet := &Fleet{}
	first := answering(t, fleet, 0, "")

	response := fanoutOver(t, fleet, first)
	require.Len(t, response.Results, 1)
	assert.Equal(t, "ok", response.Results[0].Status)

	// An empty message is still a message: it has a hash and a payload of its own.
	require.Len(t, response.Payloads, 1)
	assert.NotEmpty(t, response.PayloadIDs[0])
	assert.Contains(t, string(response.Payloads[0]), "responses")
}
