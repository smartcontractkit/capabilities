package don2don

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode"

	"google.golang.org/protobuf/proto"

	don2dontypes "github.com/smartcontractkit/capabilities/libs/x/don2don/types"
	rage "github.com/smartcontractkit/capabilities/libs/x/rage"
)

const (
	maxLoggedStringLen = 256
)

func ValidateMessage(msg *rage.Message, expectedReceiver rage.PeerID) (*don2dontypes.MessageBody, error) {
	var topLevelMessage don2dontypes.Message
	err := proto.Unmarshal(msg.Payload, &topLevelMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal message, err: %w", err)
	}
	var body don2dontypes.MessageBody
	err = proto.Unmarshal(topLevelMessage.Body, &body)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal message body, err: %w", err)
	}
	if len(body.Sender) != rage.PeerIDLength || len(body.Receiver) != rage.PeerIDLength {
		return &body, fmt.Errorf("invalid sender length (%d) or receiver length (%d)", len(body.Sender), len(body.Receiver))
	}
	if !ed25519.Verify(body.Sender, topLevelMessage.Body, topLevelMessage.Signature) {
		return &body, errors.New("failed to verify message signature")
	}
	// NOTE we currently don't support relaying messages so the p2p message sender needs to be the message author
	if !bytes.Equal(body.Sender, msg.Sender[:]) {
		return &body, errors.New("sender in message body does not match sender of p2p message")
	}
	if !bytes.Equal(body.Receiver, expectedReceiver[:]) {
		return &body, errors.New("receiver in message body does not match expected receiver")
	}
	return &body, nil
}

func ToPeerID(peerID []byte) (rage.PeerID, error) {
	if len(peerID) != rage.PeerIDLength {
		return rage.PeerID{}, fmt.Errorf("invalid peer ID length: %d", len(peerID))
	}

	var id rage.PeerID
	copy(id[:], peerID)
	return id, nil
}

func SanitizeLogString(s string) string {
	tooLongSuffix := ""
	if len(s) > maxLoggedStringLen {
		s = s[:maxLoggedStringLen]
		tooLongSuffix = " [TRUNCATED]"
	}
	for i := 0; i < len(s); i++ {
		if !unicode.IsPrint(rune(s[i])) {
			return "[UNPRINTABLE] " + hex.EncodeToString([]byte(s)) + tooLongSuffix
		}
	}
	return s + tooLongSuffix
}
