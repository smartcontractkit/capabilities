package jwt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/golang-jwt/jwt/v5"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
)

// Issue mints the token a customer's tooling sends with a request.
//
// It is the other half of Verify, and it lives here so that the two are read
// together: what is signed, and in what order, is the whole of the scheme. The
// caller supplies the token's ID, since refusing a second use of one is the
// gateway's job and minting a fresh one is theirs.
//
// lifetime is how long the token is good for, and must be no more than
// MaxExpiryDuration - a gateway refuses a token that claims longer.
func Issue[T any](key *secp256k1.PrivateKey, id string, req jsonrpc.Request[T], lifetime time.Duration) (string, error) {
	if lifetime > MaxExpiryDuration {
		return "", fmt.Errorf("a token may live at most %s, not %s", MaxExpiryDuration, lifetime)
	}

	digest, err := req.Digest()
	if err != nil {
		return "", fmt.Errorf("failed to digest the request the token is for: %w", err)
	}

	now := time.Now()
	header, err := segment(map[string]string{"alg": Alg, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := segment(Claims{
		Digest: "0x" + digest,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        id,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(lifetime)),
		},
	})
	if err != nil {
		return "", err
	}

	// Signed as an Ethereum personal signature over the two segments, and carried
	// as r ‖ s ‖ v with v as 0 or 1: the shape Recover reads.
	signing := header + "." + payload
	compact := dcrecdsa.SignCompact(key, personalHash([]byte(signing)), false)
	signature := append(append([]byte(nil), compact[1:]...), compact[0]-27)

	return signing + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// Address is the account a key signs as, which is what a workflow authorises.
func Address(key *secp256k1.PrivateKey) (string, error) {
	return auth.AddressOf(key.PubKey().SerializeUncompressed())
}

func segment(v any) (string, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to encode a token segment: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}
