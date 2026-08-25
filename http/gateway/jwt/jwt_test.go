package jwt_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
	"github.com/smartcontractkit/capabilities/http/gateway/jwt"
)

// customer is whoever is calling the gateway: a key, and the account a workflow
// authorises by naming.
type customer struct {
	key     *secp256k1.PrivateKey
	address string
}

func newCustomer(t *testing.T) customer {
	t.Helper()

	key, err := secp256k1.GeneratePrivateKey()
	require.NoError(t, err)

	address, err := auth.AddressOf(key.PubKey().SerializeUncompressed())
	require.NoError(t, err)
	return customer{key: key, address: address}
}

// token mints one the way a customer's tooling does: an ETH-alg JWT over the
// request's digest, signed as a personal message.
func (c customer) token(t *testing.T, req jsonrpc.Request[json.RawMessage], mutate ...func(*jwt.Claims)) string {
	t.Helper()

	digest, err := req.Digest()
	require.NoError(t, err)

	now := time.Now()
	claims := jwt.Claims{
		Digest: "0x" + digest,
		RegisteredClaims: gojwt.RegisteredClaims{
			ID:        uuid.NewString(),
			ExpiresAt: gojwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt:  gojwt.NewNumericDate(now),
		},
	}
	for _, m := range mutate {
		m(&claims)
	}

	header := encode(t, map[string]string{"alg": jwt.Alg, "typ": "JWT"})
	payload := encode(t, claims)
	signing := header + "." + payload

	return signing + "." + base64.RawURLEncoding.EncodeToString(c.sign(t, signing))
}

// sign is an Ethereum personal signature: keccak over the prefix, the length and
// the message, then secp256k1, with the recovery byte last.
func (c customer) sign(t *testing.T, message string) []byte {
	t.Helper()

	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	compact := dcrecdsa.SignCompact(c.key, auth.Hash([]byte(prefixed)), false)

	signature := append([]byte(nil), compact[1:]...)
	return append(signature, compact[0]-27)
}

func encode(t *testing.T, v any) string {
	t.Helper()

	encoded, err := json.Marshal(v)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func request() jsonrpc.Request[json.RawMessage] {
	params := json.RawMessage(`{"workflow":{"workflowOwner":"0xowner","workflowName":"demo"},"input":{}}`)
	return jsonrpc.Request[json.RawMessage]{Version: "2.0", ID: "1", Method: "workflows.execute", Params: &params}
}

// TestVerify is the happy path: a token for this request, signed by this
// customer, names that customer.
func TestVerify(t *testing.T) {
	c := newCustomer(t)
	req := request()

	claims, signer, err := jwt.Verify(c.token(t, req), req)
	require.NoError(t, err)
	assert.Equal(t, strings.ToLower(c.address), signer)
	assert.NotEmpty(t, claims.ID, "the jti is what lets a gateway refuse a second use")
}

// TestVerifyRejects covers every reason a token is not a licence to run this
// request. Each of these is a way in that would otherwise be open.
func TestVerifyRejects(t *testing.T) {
	c := newCustomer(t)
	req := request()

	t.Run("a token minted for another request", func(t *testing.T) {
		other := request()
		params := json.RawMessage(`{"workflow":{"workflowOwner":"0xowner","workflowName":"other"},"input":{}}`)
		other.Params = &params

		_, _, err := jwt.Verify(c.token(t, other), req)
		require.ErrorContains(t, err, "does not match calculated request digest")
	})

	t.Run("a token whose signature was replaced", func(t *testing.T) {
		token := c.token(t, req)
		parts := strings.Split(token, ".")

		stranger := newCustomer(t)
		parts[2] = base64.RawURLEncoding.EncodeToString(stranger.sign(t, parts[0]+"."+parts[1]))

		// It verifies as the stranger rather than as the customer: a signature says who,
		// and the caller is what decides whether that who is allowed.
		_, signer, err := jwt.Verify(strings.Join(parts, "."), req)
		require.NoError(t, err)
		assert.Equal(t, strings.ToLower(stranger.address), signer)
		assert.NotEqual(t, strings.ToLower(c.address), signer)
	})

	t.Run("a token that has expired", func(t *testing.T) {
		token := c.token(t, req, func(claims *jwt.Claims) {
			issued := time.Now().Add(-2 * time.Minute)
			claims.IssuedAt = gojwt.NewNumericDate(issued)
			claims.ExpiresAt = gojwt.NewNumericDate(issued.Add(time.Minute))
		})

		_, _, err := jwt.Verify(token, req)
		require.ErrorIs(t, err, gojwt.ErrTokenExpired)
	})

	t.Run("a token that would live too long", func(t *testing.T) {
		token := c.token(t, req, func(claims *jwt.Claims) {
			claims.ExpiresAt = gojwt.NewNumericDate(claims.IssuedAt.Add(time.Hour))
		})

		_, _, err := jwt.Verify(token, req)
		require.ErrorContains(t, err, "exceeds the maximum allowed")
	})

	t.Run("a token issued in the future", func(t *testing.T) {
		token := c.token(t, req, func(claims *jwt.Claims) {
			issued := time.Now().Add(time.Hour)
			claims.IssuedAt = gojwt.NewNumericDate(issued)
			claims.ExpiresAt = gojwt.NewNumericDate(issued.Add(time.Minute))
		})

		_, _, err := jwt.Verify(token, req)
		require.Error(t, err)
	})

	t.Run("a token with no ID to remember it by", func(t *testing.T) {
		token := c.token(t, req, func(claims *jwt.Claims) { claims.ID = "" })

		_, _, err := jwt.Verify(token, req)
		require.ErrorContains(t, err, "jti")
	})

	t.Run("a token signed with something other than an Ethereum key", func(t *testing.T) {
		signed, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, gojwt.RegisteredClaims{
			ID:        uuid.NewString(),
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Minute)),
			IssuedAt:  gojwt.NewNumericDate(time.Now()),
		}).SignedString([]byte("not a key of anyone's"))
		require.NoError(t, err)

		_, _, err = jwt.Verify(signed, req)
		require.Error(t, err)
	})

	t.Run("something that is not a token at all", func(t *testing.T) {
		_, _, err := jwt.Verify("nonsense", req)
		require.ErrorContains(t, err, "expected 3 parts")
	})
}
