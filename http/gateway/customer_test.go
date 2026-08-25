package main_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
	cregatewayjwt "github.com/smartcontractkit/capabilities/http/gateway/jwt"
)

// customer is whoever calls the gateway. A key and an address, like a node -
// what differs is which side of the gateway it is on, and that the workflow, not
// the DON, is what says the address may act.
type customer = node

func newCustomer(t *testing.T) customer { return newNode(t) }

// jwt mints the token a customer's tooling sends: ETH-alg, over the request's
// digest, with an ID so the gateway can refuse a second use.
func (n node) jwt(t *testing.T, req *jsonrpc.Request[json.RawMessage]) string {
	t.Helper()

	digest, err := req.Digest()
	require.NoError(t, err)

	now := time.Now()
	header := encodeJSON(t, map[string]string{"alg": cregatewayjwt.Alg, "typ": "JWT"})
	payload := encodeJSON(t, cregatewayjwt.Claims{
		Digest: "0x" + digest,
		RegisteredClaims: gojwt.RegisteredClaims{
			ID:        uuid.NewString(),
			ExpiresAt: gojwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt:  gojwt.NewNumericDate(now),
		},
	})

	signing := header + "." + payload
	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(signing), signing)
	compact := dcrecdsa.SignCompact(n.key, auth.Hash([]byte(prefixed)), false)

	signature := append(append([]byte(nil), compact[1:]...), compact[0]-27)
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func encodeJSON(t *testing.T, v any) string {
	t.Helper()

	encoded, err := json.Marshal(v)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(encoded)
}
