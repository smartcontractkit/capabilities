package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	gateway "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

// actions is why the gateway fetches at all: the nodes of a DON have to agree on
// what the internet said, and cannot if each asks separately and gets a different
// answer. One request is made, cached, and served to every node that asks - which
// is what the cache is for, rather than saving traffic.
//
// A workflow that turns the cache off is saying it does not need that: see tunnel.
type actions struct {
	lggr   logger.Logger
	client *http.Client

	cache *responseCache
}

func newActions(lggr logger.Logger, client *http.Client, ttl time.Duration) *actions {
	return &actions{lggr: lggr, client: client, cache: newResponseCache(ttl)}
}

// Answer performs what a node asked for and hands it back on the node's own
// request: the workflow behind it is waiting, so there is nothing to be gained by
// telling the node about it later.
//
// The request arrives as a JSON-RPC response because that is the shape the node
// connection carries in that direction; the answer goes back as a request, which
// is the shape the capability reads. Neither is this package's choice - it is the
// protocol the capability already speaks.
func (a *actions) Answer(ctx context.Context, node string, msg *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error) {
	if msg.Result == nil {
		return nil, fmt.Errorf("node %s sent an outbound request with no payload", node)
	}

	var request gateway.OutboundHTTPRequest
	if err := json.Unmarshal(*msg.Result, &request); err != nil {
		return nil, fmt.Errorf("node %s sent something that is not an outbound HTTP request: %w", node, err)
	}

	// Not the asking node's context to cancel: one fetch answers every node of the
	// DON that asked for the same thing, and the one that happened to start it
	// giving up - a workflow timeout, a connection that broke - would otherwise take
	// the answer away from the rest. What bounds the fetch is the request's own
	// timeout, which is the workflow's rather than this node's.
	response := a.fetch(context.WithoutCancel(ctx), request)

	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the response for node %s: %w", node, err)
	}
	params := json.RawMessage(encoded)

	return &jsonrpc.Request[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      msg.ID,
		Method:  gateway.MethodHTTPAction,
		Params:  &params,
	}, nil
}

// fetch makes the request, through the cache when the workflow asked for that.
func (a *actions) fetch(ctx context.Context, request gateway.OutboundHTTPRequest) gateway.OutboundHTTPResponse {
	// A workflow that neither wants to store nor to read is one making its own
	// request, and there is nothing for the cache to do with it.
	if !wantsCache(request.CacheSettings) {
		return a.perform(ctx, request)
	}

	return a.cache.fetch(request, func() gateway.OutboundHTTPResponse {
		return a.perform(ctx, request)
	})
}

// perform is the request itself.
func (a *actions) perform(ctx context.Context, request gateway.OutboundHTTPRequest) gateway.OutboundHTTPResponse {
	timeout := time.Duration(request.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, request.Method, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		return gateway.OutboundHTTPResponse{ErrorMessage: err.Error()}
	}
	for key, value := range request.Headers {
		req.Header.Set(key, value)
	}
	for key, values := range request.MultiHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return gateway.OutboundHTTPResponse{ErrorMessage: err.Error()}
	}
	defer resp.Body.Close()

	limit := request.MaxResponseBytes
	if limit == 0 {
		limit = 5 * 1024 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit))) //#nosec G115 - a response limit is small
	if err != nil {
		return gateway.OutboundHTTPResponse{ErrorMessage: err.Error()}
	}

	headers := make(map[string]string, len(resp.Header))
	multi := make(map[string][]string, len(resp.Header))
	for key, values := range resp.Header {
		multi[key] = values
		headers[key] = strings.Join(values, ",")
	}

	return gateway.OutboundHTTPResponse{
		StatusCode:   resp.StatusCode,
		Headers:      headers,
		MultiHeaders: multi,
		Body:         body,
	}
}

// responseCache is what makes one fetch serve a DON.
//
// Every node of the DON asks for the same thing at about the same time, and they
// have to agree on the answer. The first to ask fetches; the rest wait for that
// fetch and are given its result - so what they agree on is a fact about one
// moment rather than several.
type responseCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	// done is closed when the fetch this entry represents has finished.
	done     chan struct{}
	response gateway.OutboundHTTPResponse
	at       time.Time
}

func newResponseCache(ttl time.Duration) *responseCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &responseCache{ttl: ttl, entries: map[string]*cacheEntry{}}
}

// fetch returns the cached response, or performs one fetch on behalf of everyone
// waiting for it.
func (c *responseCache) fetch(request gateway.OutboundHTTPRequest, perform func() gateway.OutboundHTTPResponse) gateway.OutboundHTTPResponse {
	key := cacheKey(request)
	maxAge := time.Duration(request.CacheSettings.MaxAgeMs) * time.Millisecond

	c.mu.Lock()
	entry, cached := c.entries[key]
	if cached {
		c.mu.Unlock()
		<-entry.done

		age := time.Since(entry.at)
		if age <= c.ttl && (maxAge == 0 || age <= maxAge) {
			return entry.response
		}

		// Too old for this caller: replace it rather than serve it, so that the next
		// caller gets what this one is about to fetch.
		c.mu.Lock()
		if current, ok := c.entries[key]; ok && current == entry {
			delete(c.entries, key)
		}
	}

	entry = &cacheEntry{done: make(chan struct{})}
	c.entries[key] = entry
	c.mu.Unlock()

	entry.response = perform()
	entry.at = time.Now()
	close(entry.done)

	// Only what is worth remembering: an error is a fact about this moment, and a
	// server error now is not a reason to answer everyone with it for ten minutes.
	if !request.CacheSettings.Store || !cacheable(entry.response) {
		c.mu.Lock()
		if current, ok := c.entries[key]; ok && current == entry {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	}

	return entry.response
}

// expire drops what has aged out.
func (c *responseCache) expire() {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		select {
		case <-entry.done:
			if now.Sub(entry.at) > c.ttl {
				delete(c.entries, key)
			}
		default:
			// Still being fetched; whoever is waiting for it will decide.
		}
	}
}

// wantsCache reports whether the workflow asked for the cache at all: to store
// what comes back, or to be given what is already there.
func wantsCache(settings gateway.CacheSettings) bool {
	return settings.Store || settings.MaxAgeMs > 0
}

// cacheable says whether a response is worth remembering: an answer the far side
// meant (2xx) or a refusal that will not change by being asked again (4xx).
func cacheable(response gateway.OutboundHTTPResponse) bool {
	if response.ErrorMessage != "" {
		return false
	}
	return (200 <= response.StatusCode && response.StatusCode < 300) ||
		(400 <= response.StatusCode && response.StatusCode < 500)
}

// cacheKey is what makes two requests the same request: everything about them
// that the far side would see.
func cacheKey(request gateway.OutboundHTTPRequest) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\n%s\n", request.Method, request.URL)

	for _, key := range sortedKeys(request.Headers) {
		fmt.Fprintf(hash, "%s:%s\n", key, request.Headers[key])
	}
	for _, key := range sortedKeys(request.MultiHeaders) {
		for _, value := range request.MultiHeaders[key] {
			fmt.Fprintf(hash, "%s:%s\n", key, value)
		}
	}
	hash.Write(request.Body)

	return hex.EncodeToString(hash.Sum(nil))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
