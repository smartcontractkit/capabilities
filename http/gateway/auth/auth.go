// Package auth is how a gateway knows which node it is talking to.
//
// It is the scheme the node's websocket connection uses today, byte for byte:
// the node signs a header naming the moment, its DON and the gateway it means to
// reach; the gateway answers with a random challenge; the node signs that too.
// Both signatures are secp256k1, made with the node's chain key - the same key
// the DON's membership is recorded under - so what proves identity here is what
// the registry already knows about that node, rather than a second credential
// issued for the purpose.
//
// The transport changed - HTTP rather than a websocket - and this did not. A
// signed header can be replayed by anyone who sees it, for as long as its
// timestamp is tolerated; a signature over a challenge the gateway chose cannot.
// That is what makes this safe to run over a connection that is not itself
// encrypted, which the proxy hop is.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"slices"

	"golang.org/x/crypto/sha3"
)

// The layout of a packed auth header, in bytes. These are the lengths the
// gateway's own handshake uses (core's gateway/network/constants.go), kept so
// that a header produced here is the header that code would produce.
const (
	TimestampLen  = 4
	DonIDLen      = 64
	GatewayIDLen  = 128
	SignatureLen  = 65
	AuthHeaderLen = TimestampLen + DonIDLen + GatewayIDLen + SignatureLen

	// ChallengeLen is how many random bytes a challenge carries. Long enough that
	// guessing one is not a strategy.
	ChallengeLen = 32

	// challengeMinLen is the shortest thing that could be a challenge: the fixed
	// prefix plus at least one random byte.
	challengeMinLen = TimestampLen + GatewayIDLen + 1
)

// DefaultTimestampTolerance is how far from now a header's timestamp may be. It
// bounds how long a captured header could be replayed if the challenge step were
// ever skipped, and it has to be wide enough for ordinary clock drift.
const DefaultTimestampTolerance = 30 * time.Second

var (
	ErrHeaderLength    = errors.New("auth header has the wrong length")
	ErrChallengeLength = errors.New("challenge is too short")
	ErrUnknownDON      = errors.New("unknown DON")
	ErrUnknownNode     = errors.New("unknown node")
	ErrWrongGateway    = errors.New("auth header is for another gateway")
	ErrStaleTimestamp  = errors.New("timestamp is outside the tolerated range")
	ErrBadSignature    = errors.New("signature is not from a node of this DON")
	ErrFieldTooLong    = errors.New("field is longer than its fixed width")
)

// Header is what a node says about itself before it has proved anything.
type Header struct {
	// Timestamp is when the node signed this, in seconds. It bounds replay.
	Timestamp uint32

	// DonID is the DON the node claims to belong to, and GatewayID the gateway it
	// means to be talking to - so a header captured by one gateway cannot be replayed
	// at another.
	DonID     string
	GatewayID string
}

// Challenge is what the gateway answers a header with: something the node could
// not have signed in advance.
type Challenge struct {
	Timestamp uint32
	GatewayID string
	Random    []byte
}

// Signer signs on behalf of a node. It is the one thing this package cannot do
// for itself: the key lives in another process (crecore), reached over its
// keystore service, or is derived from an instance index for a local run.
//
// What it is handed is already hashed - see Hash - because that is the shape the
// keystore service signs: a digest in, a signature out, and the key stays where
// it is.
type Signer interface {
	Sign(ctx context.Context, hash []byte) ([]byte, error)
}

// Verifier says whether a signature over a hash was made by one of the nodes it
// knows, and which one.
//
// It is an interface because who the nodes are is the gateway's configuration,
// not this package's business.
type Verifier interface {
	// Nodes returns the addresses that count as members of donID, as 0x-prefixed
	// hex, or false if the DON is not one this gateway serves.
	Nodes(donID string) ([]string, bool)

	// Verify reports whether sig over hash was made by the key behind address.
	Verify(address string, hash, sig []byte) bool
}

// PackHeader lays a header out for signing. The signature covers exactly these
// bytes, and is appended to them to make the header that travels.
func PackHeader(h Header) ([]byte, error) {
	don, err := fixed(h.DonID, DonIDLen)
	if err != nil {
		return nil, fmt.Errorf("don ID: %w", err)
	}
	gateway, err := fixed(h.GatewayID, GatewayIDLen)
	if err != nil {
		return nil, fmt.Errorf("gateway ID: %w", err)
	}

	packed := make([]byte, 0, AuthHeaderLen-SignatureLen)
	packed = binary.BigEndian.AppendUint32(packed, h.Timestamp)
	packed = append(packed, don...)
	packed = append(packed, gateway...)
	return packed, nil
}

// UnpackHeader splits a signed header back into what was said and what signed it.
func UnpackHeader(data []byte) (Header, []byte, error) {
	if len(data) != AuthHeaderLen {
		return Header{}, nil, fmt.Errorf("%w: got %d, want %d", ErrHeaderLength, len(data), AuthHeaderLen)
	}

	offset := 0
	h := Header{Timestamp: binary.BigEndian.Uint32(data[offset : offset+TimestampLen])}
	offset += TimestampLen
	h.DonID = unfixed(data[offset : offset+DonIDLen])
	offset += DonIDLen
	h.GatewayID = unfixed(data[offset : offset+GatewayIDLen])

	return h, data[AuthHeaderLen-SignatureLen:], nil
}

// PackChallenge lays a challenge out for signing.
func PackChallenge(c Challenge) ([]byte, error) {
	gateway, err := fixed(c.GatewayID, GatewayIDLen)
	if err != nil {
		return nil, fmt.Errorf("gateway ID: %w", err)
	}

	packed := make([]byte, 0, TimestampLen+GatewayIDLen+len(c.Random))
	packed = binary.BigEndian.AppendUint32(packed, c.Timestamp)
	packed = append(packed, gateway...)
	return append(packed, c.Random...), nil
}

// UnpackChallenge reads a challenge, for a node that wants to check which gateway
// and which moment it is about to sign for.
func UnpackChallenge(data []byte) (Challenge, error) {
	if len(data) < challengeMinLen {
		return Challenge{}, fmt.Errorf("%w: got %d, want at least %d", ErrChallengeLength, len(data), challengeMinLen)
	}

	c := Challenge{Timestamp: binary.BigEndian.Uint32(data[:TimestampLen])}
	c.GatewayID = unfixed(data[TimestampLen : TimestampLen+GatewayIDLen])
	c.Random = data[TimestampLen+GatewayIDLen:]
	return c, nil
}

// NewChallenge returns a challenge for this gateway, now.
func NewChallenge(gatewayID string, now time.Time) (Challenge, error) {
	random := make([]byte, ChallengeLen)
	if _, err := rand.Read(random); err != nil {
		return Challenge{}, fmt.Errorf("failed to read randomness for a challenge: %w", err)
	}
	return Challenge{
		Timestamp: uint32(now.Unix()), //#nosec G115 - seconds since the epoch, until 2106
		GatewayID: gatewayID,
		Random:    random,
	}, nil
}

// Hash is what is actually signed: keccak256 of the packed bytes, which is what
// a chain key signs everywhere else in this system.
func Hash(data []byte) []byte {
	hash := sha3.NewLegacyKeccak256()
	hash.Write(data)
	return hash.Sum(nil)
}

// fixed right-pads s into a field of n bytes, rejecting anything that would not
// fit rather than silently truncating an identity.
func fixed(s string, n int) ([]byte, error) {
	if len(s) > n {
		return nil, fmt.Errorf("%w: %d bytes into %d", ErrFieldTooLong, len(s), n)
	}
	field := make([]byte, n)
	copy(field, s)
	return field, nil
}

// unfixed reads a right-padded field back.
func unfixed(field []byte) string {
	for i, b := range field {
		if b == 0 {
			return string(field[:i])
		}
	}
	return string(field)
}

// VerifyHeader checks a signed header and says which node sent it.
//
// Everything a gateway can check without a round trip is checked here: that the
// DON is one it serves, that the header is addressed to it rather than to
// another gateway, that the timestamp is recent, and that the signature is from a
// member of that DON. What it cannot check is whether the sender holds the key
// now rather than having copied a header - which is what the challenge is for.
func VerifyHeader(v Verifier, gatewayID string, data []byte, now time.Time, tolerance time.Duration) (Header, string, error) {
	header, signature, err := UnpackHeader(data)
	if err != nil {
		return Header{}, "", err
	}
	if header.GatewayID != gatewayID {
		return Header{}, "", fmt.Errorf("%w: %q is not %q", ErrWrongGateway, header.GatewayID, gatewayID)
	}

	skew := now.Sub(time.Unix(int64(header.Timestamp), 0))
	if skew < -tolerance || tolerance < skew {
		return Header{}, "", fmt.Errorf("%w: %s off", ErrStaleTimestamp, skew)
	}

	nodes, ok := v.Nodes(header.DonID)
	if !ok {
		return Header{}, "", fmt.Errorf("%w: %q", ErrUnknownDON, header.DonID)
	}

	node, err := signedBy(v, nodes, Hash(data[:AuthHeaderLen-SignatureLen]), signature)
	if err != nil {
		return Header{}, "", err
	}
	return header, node, nil
}

// VerifyChallengeResponse checks that the node the header claimed to be from is
// the one on the other end of this connection, now.
func VerifyChallengeResponse(v Verifier, donID, node string, challenge, signature []byte) error {
	nodes, ok := v.Nodes(donID)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownDON, donID)
	}
	if !slices.Contains(nodes, node) {
		return fmt.Errorf("%w: %q", ErrUnknownNode, node)
	}
	if !v.Verify(node, Hash(challenge), signature) {
		return fmt.Errorf("%w: the challenge was not signed by %s", ErrBadSignature, node)
	}
	return nil
}

// signedBy returns which of nodes signed hash.
//
// Tried one at a time rather than recovered, because the answer only matters if
// it is one of these: a DON is a handful of nodes, and this runs when a
// connection is made rather than when it is used.
func signedBy(v Verifier, nodes []string, hash, signature []byte) (string, error) {
	for _, node := range nodes {
		if v.Verify(node, hash, signature) {
			return node, nil
		}
	}
	return "", ErrBadSignature
}
