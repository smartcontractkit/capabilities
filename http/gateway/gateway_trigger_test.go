package main_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	gatewaytypes "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

	"github.com/smartcontractkit/capabilities/http/gateway/inproc"
	"github.com/smartcontractkit/capabilities/http/gateway/service"
)

const workflowID = "0x1111111111111111111111111111111111111111111111111111111111111111"

// don is a gateway and the nodes it serves, wired in process.
type don struct {
	gateway  *service.Gateway
	customer customer
	nodes    []*inproc.Connector
	answers  []*answering
}

// answering is a node's end: it answers triggers with what it is told to, and
// reports the workflows it is told it runs.
type answering struct {
	metadata []gatewaytypes.WorkflowMetadata

	// push sends each workflow on its own, the way the capability does when a
	// workflow registers, rather than as the batch a pull is answered with.
	push   bool
	answer func(req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage]

	connector *inproc.Connector
	gatewayID string
	node      string
}

func (a *answering) ID(context.Context) (string, error) { return a.node, nil }

func (a *answering) HandleGatewayMessage(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) error {
	switch req.Method {
	case gatewaytypes.MethodPullWorkflowMetadata:
		if a.push {
			for _, workflow := range a.metadata {
				if err := a.report(ctx, gatewayID, req.ID, workflow); err != nil {
					return err
				}
			}
			return nil
		}
		return a.report(ctx, gatewayID, req.ID, a.metadata)
	default:
		return a.connector.SendToGateway(ctx, gatewayID, a.answer(req))
	}
}

// report sends the gateway what this node runs, in whatever shape it was given:
// one workflow, as a push does, or a slice of them, as a pull's answer does.
func (a *answering) report(ctx context.Context, gatewayID, id string, workflows any) error {
	encoded, err := json.Marshal(workflows)
	if err != nil {
		return err
	}
	result := json.RawMessage(encoded)
	return a.connector.SendToGateway(ctx, gatewayID, &jsonrpc.Response[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      id,
		Method:  gatewaytypes.MethodPushWorkflowMetadata,
		Result:  &result,
	})
}

// newDON starts a gateway with count nodes, each of which runs the one workflow
// the customer will trigger.
func newDON(t *testing.T, count, f int, answer func(node string, req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage], opts ...func(*answering)) *don {
	t.Helper()

	customer := newCustomer(t)
	nodes := inproc.NewNodes(logger.Test(t))

	gateway, err := service.New(logger.Test(t), service.Config{
		GatewayID:        gatewayID,
		DonID:            donID,
		F:                f,
		RequestTimeout:   5 * time.Second,
		MetadataInterval: 50 * time.Millisecond,
	}, nodes)
	require.NoError(t, err)

	d := &don{gateway: gateway}
	for range count {
		address := newNode(t).address
		connector := nodes.Connector(gateway, gatewayID, donID, address)

		handler := &answering{
			connector: connector,
			gatewayID: gatewayID,
			node:      address,
			metadata: []gatewaytypes.WorkflowMetadata{{
				WorkflowSelector: gatewaytypes.WorkflowSelector{
					WorkflowID:    workflowID,
					WorkflowOwner: customer.address,
					WorkflowName:  "demo",
					WorkflowTag:   "v1",
				},
				AuthorizedKeys: []gatewaytypes.AuthorizedKey{{
					KeyType:   gatewaytypes.KeyTypeECDSAEVM,
					PublicKey: customer.address,
				}},
			}},
			answer: func(req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage] {
				return answer(address, req)
			},
		}
		for _, opt := range opts {
			opt(handler)
		}

		require.NoError(t, connector.AddHandler(t.Context(), []string{
			gatewaytypes.MethodWorkflowExecute,
			gatewaytypes.MethodPullWorkflowMetadata,
		}, handler))

		d.nodes = append(d.nodes, connector)
		d.answers = append(d.answers, handler)
	}

	servicetest.Run(t, gateway)

	d.customer = customer

	// The gateway learns what the DON runs by asking, so nothing can be triggered
	// until enough nodes have answered. Asked with a fresh token each time, since a
	// token authorises one request.
	require.Eventually(t, func() bool {
		response := gateway.Handle(t.Context(), trigger(t, customer, "probe-"+time.Now().Format(time.RFC3339Nano)))
		return response.Error == nil
	}, 5*time.Second, 20*time.Millisecond, "the gateway never learned which workflows the DON runs")

	return d
}

// trigger is what a customer sends: a JSON-RPC request naming a workflow, with a
// JWT over its digest.
func trigger(t *testing.T, c customer, id string) *jsonrpc.Request[json.RawMessage] {
	params, _ := json.Marshal(gatewaytypes.HTTPTriggerRequest{
		Input: json.RawMessage(`{"hello":"world"}`),
		Workflow: gatewaytypes.WorkflowSelector{
			WorkflowOwner: c.address,
			WorkflowName:  "demo",
			WorkflowTag:   "v1",
		},
	})
	raw := json.RawMessage(params)

	req := &jsonrpc.Request[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      id,
		Method:  gatewaytypes.MethodWorkflowExecute,
		Params:  &raw,
	}
	req.Auth = c.jwt(t, req)
	return req
}

// accepted is what a node answers a trigger with.
func accepted(req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage] {
	result, _ := json.Marshal(gatewaytypes.HTTPTriggerResponse{
		WorkflowID:          workflowID,
		WorkflowExecutionID: "execution-1",
		Status:              gatewaytypes.HTTPTriggerStatusAccepted,
	})
	raw := json.RawMessage(result)
	return &jsonrpc.Response[json.RawMessage]{Version: jsonrpc.JsonRpcVersion, ID: req.ID, Result: &raw}
}

// TestTrigger is the customer's round trip: a signed request reaches the DON, and
// the answer comes back once enough of it agrees.
func TestTrigger(t *testing.T) {
	d := newDON(t, 4, 1, func(_ string, req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage] {
		return accepted(req)
	})

	response := d.gateway.Handle(t.Context(), trigger(t, d.customer, "1"))
	require.Nil(t, response.Error, "the request should have been accepted")

	var answer gatewaytypes.HTTPTriggerResponse
	require.NoError(t, json.Unmarshal(*response.Result, &answer))
	assert.Equal(t, gatewaytypes.HTTPTriggerStatusAccepted, answer.Status)
	assert.Equal(t, "execution-1", answer.WorkflowExecutionID)
}

// TestTriggerNeedsAgreement is the reason the gateway waits: one node's answer is
// one node's word, and a DON that cannot muster F+1 of the same answer has not
// answered.
func TestTriggerNeedsAgreement(t *testing.T) {
	var honest int
	d := newDON(t, 4, 1, func(node string, req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage] {
		// One node says something different from the rest - and it says it first.
		if honest == 0 {
			honest++
			result := json.RawMessage(`{"workflow_execution_id":"a-lie"}`)
			return &jsonrpc.Response[json.RawMessage]{Version: jsonrpc.JsonRpcVersion, ID: req.ID, Result: &result}
		}
		return accepted(req)
	})

	response := d.gateway.Handle(t.Context(), trigger(t, d.customer, "2"))
	require.Nil(t, response.Error)

	var answer gatewaytypes.HTTPTriggerResponse
	require.NoError(t, json.Unmarshal(*response.Result, &answer))
	assert.Equal(t, "execution-1", answer.WorkflowExecutionID, "the answer must be the one the DON agreed on")
}

// TestTriggerRejects covers what the gateway refuses before the DON hears
// anything at all.
func TestTriggerRejects(t *testing.T) {
	d := newDON(t, 4, 1, func(_ string, req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage] {
		return accepted(req)
	})

	t.Run("a request signed by someone the workflow does not authorise", func(t *testing.T) {
		stranger := newCustomer(t)
		req := trigger(t, d.customer, "3")
		req.Auth = stranger.jwt(t, req)

		response := d.gateway.Handle(t.Context(), req)
		require.NotNil(t, response.Error)
		assert.Contains(t, response.Error.Message, "is not authorized for workflow")
	})

	t.Run("a token used twice", func(t *testing.T) {
		req := trigger(t, d.customer, "4")

		first := d.gateway.Handle(t.Context(), req)
		require.Nil(t, first.Error)

		second := d.gateway.Handle(t.Context(), req)
		require.NotNil(t, second.Error)
		assert.Contains(t, second.Error.Message, "already been used")
	})

	t.Run("a workflow nobody runs", func(t *testing.T) {
		req := trigger(t, d.customer, "5")
		params, err := json.Marshal(gatewaytypes.HTTPTriggerRequest{
			Workflow: gatewaytypes.WorkflowSelector{WorkflowOwner: d.customer.address, WorkflowName: "not-a-workflow"},
		})
		require.NoError(t, err)
		raw := json.RawMessage(params)
		req.Params = &raw
		req.Auth = d.customer.jwt(t, req)

		response := d.gateway.Handle(t.Context(), req)
		require.NotNil(t, response.Error)
		assert.Contains(t, response.Error.Message, "no workflow matches")
	})
}

// TestTriggerFromPushedMetadata is the shape a workflow registering sends: one
// workflow on its own, rather than the batch a pull is answered with. Both are a
// node saying what it runs, and a gateway that only reads one of them is a
// gateway no newly registered workflow can be triggered on.
func TestTriggerFromPushedMetadata(t *testing.T) {
	d := newDON(t, 4, 1, func(_ string, req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage] {
		return accepted(req)
	}, func(a *answering) { a.push = true })

	response := d.gateway.Handle(t.Context(), trigger(t, d.customer, "3"))
	require.Nil(t, response.Error, "a pushed workflow is one the gateway can be asked for")

	var answer gatewaytypes.HTTPTriggerResponse
	require.NoError(t, json.Unmarshal(*response.Result, &answer))
	assert.Equal(t, gatewaytypes.HTTPTriggerStatusAccepted, answer.Status)
}
