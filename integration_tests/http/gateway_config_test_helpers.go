package http

import (
	"encoding/json"
	"strings"
)

const testDonName = "test_don"

type gatewayNode struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

func buildGatewayConfigJSON(authGatewayID string, f int, nodes []gatewayNode, handlerName string, handlerConfig json.RawMessage) string {
	for i := range nodes {
		nodes[i].Address = strings.ToLower(nodes[i].Address)
	}

	cfg := map[string]any{
		"ConnectionManagerConfig": map[string]any{
			"AuthChallengeLen":          32,
			"AuthGatewayId":             authGatewayID,
			"AuthTimestampToleranceSec": 30,
		},
		"NodeServerConfig": map[string]any{
			"Path":                   "/node",
			"Port":                   0,
			"HandshakeTimeoutMillis": 2000,
			"MaxRequestBytes":        20000,
			"ReadTimeoutMillis":      5000,
			"RequestTimeoutMillis":   5000,
			"WriteTimeoutMillis":     10000,
		},
		"UserServerConfig": map[string]any{
			"Path":                 "/user",
			"Port":                 0,
			"ContentTypeHeader":    "application/jsonrpc",
			"MaxRequestBytes":      20000,
			"ReadTimeoutMillis":    5000,
			"RequestTimeoutMillis": 5000,
			"WriteTimeoutMillis":   10000,
		},
		"ShardedDONs": []any{
			map[string]any{
				"DonName": testDonName,
				"F":       f,
				"Shards": []any{
					map[string]any{"Nodes": nodes},
				},
			},
		},
		"Services": []any{
			map[string]any{
				"ServiceName": "workflows",
				"DONs":        []string{testDonName},
				"Handlers": []any{
					map[string]any{
						"Name":        handlerName,
						"ServiceName": "workflows",
						"Config":      handlerConfig,
					},
				},
			},
		},
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func buildHTTPActionGatewayConfig(authGatewayID, publicKey string) string {
	return buildGatewayConfigJSON(
		authGatewayID,
		1,
		[]gatewayNode{{Name: "test_node_1", Address: publicKey}},
		"http-capabilities",
		json.RawMessage(`{
			"NodeRateLimiter": {
				"GlobalBurst": 50,
				"GlobalRPS": 50,
				"PerSenderBurst": 50,
				"PerSenderRPS": 50
			},
			"UserRateLimiter": {
				"GlobalBurst": 50,
				"GlobalRPS": 50,
				"PerSenderBurst": 50,
				"PerSenderRPS": 50
			}
		}`),
	)
}

func buildHTTPTriggerGatewayConfig(authGatewayID string, f int, nodes []gatewayNode) string {
	return buildGatewayConfigJSON(
		authGatewayID,
		f,
		nodes,
		"http-capabilities",
		json.RawMessage(`{
			"MaxTriggerRequestDurationMs": 5000,
			"MetadataPullIntervalMs": 1000,
			"MetadataAggregationIntervalMs": 1000,
			"NodeRateLimiter": {
				"GlobalBurst": 10,
				"GlobalRPS": 50,
				"PerSenderBurst": 10,
				"PerSenderRPS": 10
			},
			"UserRateLimiter": {
				"GlobalBurst": 10,
				"GlobalRPS": 50,
				"PerSenderBurst": 10,
				"PerSenderRPS": 10
			}
		}`),
	)
}
