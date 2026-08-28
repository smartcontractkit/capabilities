package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	gateway "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

	"github.com/smartcontractkit/capabilities/http/gateway/wire"
)

// Nodes is an interface because how a node is reached is not this package's
// business: over HTTP for a deployed gateway, or by function call where the
// gateway and the nodes are the same process. Everything above it - who may
// trigger what, when a DON has agreed, what is worth caching - is the same.
type Nodes interface {
	Send(node string, req *jsonrpc.Request[json.RawMessage]) error

	Connected(donID string) []string
}

type Config struct {
	// What a node's signed header has to name: the gateway's identity, not its
	// address.
	GatewayID string `json:"gatewayId" usage:"the name nodes authenticate to this gateway as"`

	// F is fault tolerance: a customer is answered when F+1 nodes said the same thing.
	DonID string `json:"donId" usage:"the DON this gateway serves"`
	F     int    `json:"f" usage:"the DON's fault tolerance; F+1 nodes must agree before a customer is answered"`

	RequestTimeout time.Duration `json:"requestTimeout" usage:"how long a customer's request waits for the DON to agree"`

	CacheTTL   time.Duration `json:"cacheTtl" usage:"how long an outbound HTTP response is cached"`
	StaleAfter time.Duration `json:"staleAfter" usage:"how long a node's workflow metadata counts before it is treated as gone"`

	// How long a spent JWT ID is remembered, so a token authorises one request rather
	// than every request until it expires.
	ReplayWindow time.Duration `json:"replayWindow" usage:"how long a used auth token's ID is remembered"`

	// Nodes push their metadata as it changes; asking is what catches up a gateway
	// that restarted.
	MetadataInterval time.Duration `json:"metadataInterval" usage:"how often nodes are asked which workflows they run"`
}

// Defaults are the intervals the gateway runs with today.
var Defaults = Config{
	RequestTimeout:   time.Minute,
	CacheTTL:         10 * time.Minute,
	StaleAfter:       5 * time.Minute,
	ReplayWindow:     24 * time.Hour,
	MetadataInterval: time.Minute,
}

type Gateway struct {
	services.Service
	eng *services.Engine

	lggr     logger.Logger
	cfg      Config
	nodes    Nodes
	metadata *metadata
	triggers *triggers
	actions  *actions
}

// nodes is the seam: the HTTP transport for a gateway serving a DON over the
// network, or an in-process one where the nodes are goroutines here.
func New(lggr logger.Logger, cfg Config, nodes Nodes) (*Gateway, error) {
	if cfg.GatewayID == "" {
		return nil, errors.New("a gateway needs an ID: it is what nodes sign their headers for")
	}
	if cfg.DonID == "" {
		return nil, errors.New("a gateway needs to know which DON it serves")
	}
	if nodes == nil {
		return nil, errors.New("a gateway needs connections to the nodes of its DON")
	}
	cfg = cfg.withDefaults()

	g := &Gateway{lggr: lggr, cfg: cfg, nodes: nodes}

	// F+1: enough nodes that at least one of them is honest.
	agreement := cfg.F + 1

	g.metadata = newMetadata(logger.Named(lggr, "Metadata"), agreement, cfg.StaleAfter)
	g.triggers = newTriggers(logger.Named(lggr, "Triggers"), g.metadata, nodes.Send,
		func() []string { return nodes.Connected(cfg.DonID) },
		agreement, cfg.RequestTimeout, cfg.ReplayWindow)
	g.actions = newActions(logger.Named(lggr, "Actions"), &http.Client{}, nodes.Send, cfg.CacheTTL)

	g.Service, g.eng = services.Config{
		Name:  "Gateway",
		Start: g.start,
	}.NewServiceEngine(lggr)

	return g, nil
}

func (c Config) withDefaults() Config {
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = Defaults.RequestTimeout
	}
	if c.CacheTTL <= 0 {
		c.CacheTTL = Defaults.CacheTTL
	}
	if c.StaleAfter <= 0 {
		c.StaleAfter = Defaults.StaleAfter
	}
	if c.ReplayWindow <= 0 {
		c.ReplayWindow = Defaults.ReplayWindow
	}
	if c.MetadataInterval <= 0 {
		c.MetadataInterval = Defaults.MetadataInterval
	}
	return c
}

func (g *Gateway) start(context.Context) error {
	g.eng.Go(g.keep)
	return nil
}

// keep asks the nodes what they are running, and forgets what has aged out.
//
// The asking matters for a gateway that has just started: nodes push their
// metadata as it changes, and a gateway that missed those pushes would know about
// no workflows at all until one of them changed.
func (g *Gateway) keep(ctx context.Context) {
	ticker := time.NewTicker(g.cfg.MetadataInterval)
	defer ticker.Stop()

	g.pull(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.pull(ctx)
			g.metadata.Expire()
			g.actions.cache.expire()
			g.triggers.replay.expire()
		}
	}
}

func (g *Gateway) pull(context.Context) {
	for _, node := range g.nodes.Connected(g.cfg.DonID) {
		// The method before the slash is the shape the answering capability checks for;
		// one it cannot recognise it refuses rather than answers.
		if err := g.nodes.Send(node, &jsonrpc.Request[json.RawMessage]{
			Version: jsonrpc.JsonRpcVersion,
			ID:      gateway.MethodPullWorkflowMetadata + "/" + g.cfg.GatewayID + "/" + time.Now().Format(time.RFC3339Nano),
			Method:  gateway.MethodPullWorkflowMetadata,
		}); err != nil {
			g.lggr.Debugw("Failed to ask a node for its workflows", "node", node, "err", err)
		}
	}
}

// Nothing here trusts the message about who sent it: the connection established
// that.
func (g *Gateway) HandleNodeMessage(ctx context.Context, donID, node string, msg *jsonrpc.Response[json.RawMessage]) error {
	switch msg.Method {
	case gateway.MethodHTTPAction:
		return g.actions.Handle(ctx, node, msg)
	case gateway.MethodPushWorkflowMetadata, gateway.MethodPullWorkflowMetadata:
		return g.recordMetadata(node, msg)
	default:
		// An answer to something this gateway asked for, which today is only a trigger.
		return g.triggers.Answer(node, msg)
	}
}

// A push carries one workflow and a pull's answer carries a batch, so both shapes
// are read: which it is is the node's business, and either way it is what that
// node runs.
func (g *Gateway) recordMetadata(node string, msg *jsonrpc.Response[json.RawMessage]) error {
	if msg.Result == nil {
		return fmt.Errorf("node %s sent workflow metadata with no payload", node)
	}

	var reported []gateway.WorkflowMetadata
	if err := json.Unmarshal(*msg.Result, &reported); err != nil {
		var one gateway.WorkflowMetadata
		if err := json.Unmarshal(*msg.Result, &one); err != nil {
			return fmt.Errorf("node %s sent workflow metadata that does not parse: %w", node, err)
		}
		reported = []gateway.WorkflowMetadata{one}
	}
	return g.metadata.Record(node, reported)
}

// ServeHTTP is the customer's end, on the path the gateway serves today.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "this endpoint takes JSON-RPC over POST", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read the request", http.StatusBadRequest)
		return
	}

	var req jsonrpc.Request[json.RawMessage]
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, &jsonrpc.Response[json.RawMessage]{
			Version: jsonrpc.JsonRpcVersion,
			Error:   &jsonrpc.WireError{Code: jsonrpc.ErrParse, Message: "request is not JSON-RPC: " + err.Error()},
		})
		return
	}

	writeJSON(w, g.Handle(r.Context(), &req))
}

// Handle is what an in-process caller uses instead of dialling itself.
func (g *Gateway) Handle(ctx context.Context, req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage] {
	return g.triggers.Handle(ctx, req)
}

func (g *Gateway) Routes(mux *http.ServeMux) {
	mux.Handle("POST "+wire.PathUser, g)
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
