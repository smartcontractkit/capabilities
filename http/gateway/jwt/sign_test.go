package jwt_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"

	"github.com/smartcontractkit/capabilities/http/gateway/jwt"
)

// request is what a token authorises: a trigger, with something in it, so that
// the digest is over more than a shape.
func signedRequest(t *testing.T) jsonrpc.Request[json.RawMessage] {
	t.Helper()

	params := json.RawMessage(`{"input":{"greeting":"hello"}}`)
	return jsonrpc.Request[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      "request-1",
		Method:  "workflows.execute",
		Params:  &params,
	}
}

// TestIssueVerifies is the round trip: a token this mints is one the gateway
// accepts, signed by the address the key signs as.
func TestIssueVerifies(t *testing.T) {
	key, err := secp256k1.GeneratePrivateKey()
	require.NoError(t, err)

	req := signedRequest(t)
	token, err := jwt.Issue(key, "token-1", req, time.Minute)
	require.NoError(t, err)

	claims, signer, err := jwt.Verify(token, req)
	require.NoError(t, err)
	assert.Equal(t, "token-1", claims.ID)

	address, err := jwt.Address(key)
	require.NoError(t, err)
	assert.Equal(t, address, signer, "the signer is the address the key signs as")
}

// TestIssueIsForOneRequest is what stops a token being lifted onto another
// request: the digest it carries is of the one it was minted for.
func TestIssueIsForOneRequest(t *testing.T) {
	key, err := secp256k1.GeneratePrivateKey()
	require.NoError(t, err)

	token, err := jwt.Issue(key, "token-1", signedRequest(t), time.Minute)
	require.NoError(t, err)

	other := signedRequest(t)
	params := json.RawMessage(`{"input":{"greeting":"goodbye"}}`)
	other.Params = &params

	_, _, err = jwt.Verify(token, other)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "digest")
}

// TestIssueRefusesLongLives keeps the mint on the same side of the rule as the
// check: a token no gateway would accept is not one to hand out.
func TestIssueRefusesLongLives(t *testing.T) {
	key, err := secp256k1.GeneratePrivateKey()
	require.NoError(t, err)

	_, err = jwt.Issue(key, "token-1", signedRequest(t), time.Hour)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most")
}
