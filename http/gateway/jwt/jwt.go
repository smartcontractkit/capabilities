// Package jwt is how a customer proves a trigger request is theirs.
//
// It is the scheme the gateway accepts today, unchanged, because the customer is
// on the other end of it: a JSON Web Token whose "alg" is ETH, signed over the
// token's own header and payload with an Ethereum key, carrying the digest of the
// JSON-RPC request it authorises. What the gateway does with it is recover the
// signer and ask whether that address is one the workflow authorised.
//
// It is ported rather than imported because the code it came from lives in the
// node (core/utils/jwt.go) and reaches for go-ethereum, which this repository
// does not take. The rules are the same rules - the prefix the signature is over,
// the digest claim, the lifetime bounds - and the tests pin them against tokens
// the node's own code produced.
package jwt

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
)

// Alg is what the token's header has to say. It is not a JOSE algorithm; it says
// the signature is an Ethereum personal-sign over the signing string.
const Alg = "ETH"

// ethSignedMessagePrefix is what an Ethereum personal signature covers, before
// the message and its length. A signature made for anything else - a transaction,
// a report - cannot be replayed as a token because of it.
const ethSignedMessagePrefix = "\x19Ethereum Signed Message:\n"

// Limits on what a token may claim. A token is a licence to run a workflow, so
// its life is short and its clock has to be roughly ours.
const (
	MaxExpiryDuration = 5 * time.Minute
	IssuedAtTolerance = 5 * time.Minute
)

// Claims are what a request's token carries: the digest of the request it
// authorises, and the registered claims that bound its life.
type Claims struct {
	Digest string `json:"digest"`
	jwt.RegisteredClaims
}

// Verify returns the account that signed, lowercased. Everything in it is a
// reason a token may not be used: it has to be for this request (the digest),
// alive (iat, exp), short-lived, and identified (jti) so that a second use can be
// refused. Whether that signer may run the workflow is the caller's question.
func Verify[T any](token string, req jsonrpc.Request[T]) (*Claims, string, error) {
	signed, signature, err := split(token)
	if err != nil {
		return nil, "", err
	}

	raw, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return nil, "", fmt.Errorf("the signature segment is not base64url: %w", err)
	}

	signer, err := auth.Recover(personalHash([]byte(signed)), raw)
	if err != nil {
		return nil, "", fmt.Errorf("failed to recover the signer: %w", err)
	}

	// Parsed with the recovered signer as the key, so that the library's own
	// validation - expiry, structure - runs against the same signature this recovered.
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != Alg {
			return nil, fmt.Errorf("unsupported JWT 'alg': %q, expected %q", t.Method.Alg(), Alg)
		}
		return signer, nil
	})
	if err != nil {
		return nil, "", err
	}

	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, "", errors.New("the token's claims did not validate")
	}
	if err := check(claims, req); err != nil {
		return nil, "", err
	}
	return claims, strings.ToLower(signer), nil
}

// check is about this request rather than about the token in general.
func check[T any](claims *Claims, req jsonrpc.Request[T]) error {
	switch {
	case claims.ID == "":
		return errors.New("JWT ID (jti) is required but missing")
	case claims.ExpiresAt == nil:
		return errors.New("expiresAt (exp) is required but missing")
	case claims.IssuedAt == nil:
		return errors.New("issuedAt (iat) is required but missing")
	}

	now := time.Now()
	if claims.IssuedAt.After(now.Add(IssuedAtTolerance)) {
		return fmt.Errorf("issuedAt (iat) is too far in the future (beyond tolerance of %.0f seconds)", IssuedAtTolerance.Seconds())
	}
	if lifetime := claims.ExpiresAt.Sub(claims.IssuedAt.Time); lifetime > MaxExpiryDuration {
		return fmt.Errorf("token lifetime %.0f sec exceeds the maximum allowed %.0f sec. Reduce the gap between 'iat' and 'exp'",
			lifetime.Seconds(), MaxExpiryDuration.Seconds())
	}

	digest, err := req.Digest()
	if err != nil {
		return err
	}
	if claims.Digest != "0x"+digest {
		return fmt.Errorf("claim digest '%s' does not match calculated request digest '0x%s'", claims.Digest, digest)
	}
	return nil
}

// What an Ethereum personal signature is over: prefix, length, message, keccak256.
func personalHash(message []byte) []byte {
	return auth.Hash([]byte(fmt.Sprintf("%s%d%s", ethSignedMessagePrefix, len(message), message)))
}

func split(token string) (string, string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", errors.New("invalid JWT format: expected 3 parts")
	}
	return parts[0] + "." + parts[1], parts[2], nil
}

// Registered so the parser recognises the tokens customers send. Signing is not
// implemented: a gateway verifies.
type method struct{}

func (method) Alg() string { return Alg }

func (method) Sign(string, any) ([]byte, error) {
	return nil, errors.New("a gateway verifies tokens; it does not sign them")
}

// The signature was already recovered - that is how the key got here - so this
// re-derives the address and compares.
func (method) Verify(signingString string, signature []byte, key any) error {
	address, ok := key.(string)
	if !ok {
		return jwt.ErrInvalidKeyType
	}

	recovered, err := auth.Recover(personalHash([]byte(signingString)), signature)
	if err != nil {
		return err
	}
	if !strings.EqualFold(recovered, address) {
		return jwt.ErrSignatureInvalid
	}
	return nil
}

func init() {
	jwt.RegisterSigningMethod(Alg, func() jwt.SigningMethod { return method{} })
}
