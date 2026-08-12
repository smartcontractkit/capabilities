package don2don_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/capabilities/libs/x/don2don"
	don2dontypes "github.com/smartcontractkit/capabilities/libs/x/don2don/types"
	rage "github.com/smartcontractkit/capabilities/libs/x/rage"
)

const (
	capID1   = "cap1"
	capID2   = "cap2"
	donID1   = uint32(1)
	payload1 = "hello world"
	payload2 = "goodbye world"
)

func TestValidateMessage(t *testing.T) {
	t.Parallel()

	privKey1, peerID1 := newKeyPair(t)
	_, peerID2 := newKeyPair(t)

	// valid
	p2pMsg := encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload1))
	body, err := don2don.ValidateMessage(&p2pMsg, peerID2)
	require.NoError(t, err)
	require.Equal(t, peerID1[:], body.Sender)
	require.Equal(t, payload1, string(body.Payload))

	// invalid sender
	p2pMsg = encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload1))
	p2pMsg.Sender = peerID2
	_, err = don2don.ValidateMessage(&p2pMsg, peerID2)
	require.Error(t, err)

	// invalid receiver
	p2pMsg = encodeAndSign(t, privKey1, peerID1, peerID2, capID1, donID1, []byte(payload1))
	_, err = don2don.ValidateMessage(&p2pMsg, peerID1)
	require.Error(t, err)
}

func newKeyPair(t *testing.T) (ed25519.PrivateKey, ragetypes.PeerID) {
	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	peerID, err := ragetypes.PeerIDFromPrivateKey(privKey)
	require.NoError(t, err)
	return privKey, peerID
}

func encodeAndSign(t *testing.T, senderPrivKey ed25519.PrivateKey, senderID rage.PeerID, receiverID rage.PeerID, capabilityID string, donID uint32, payload []byte) rage.Message {
	body := &don2dontypes.MessageBody{
		Sender:          senderID[:],
		Receiver:        receiverID[:],
		CapabilityId:    capabilityID,
		CapabilityDonId: donID,
		Payload:         payload,
	}
	return signBody(t, senderPrivKey, body, senderID)
}

func encodeAndSignForMethod(t *testing.T, senderPrivKey ed25519.PrivateKey, senderID rage.PeerID, receiverID rage.PeerID, capabilityID string, methodName string, donID uint32, payload []byte) rage.Message {
	body := &don2dontypes.MessageBody{
		Sender:           senderID[:],
		Receiver:         receiverID[:],
		CapabilityId:     capabilityID,
		CapabilityMethod: methodName,
		CapabilityDonId:  donID,
		Payload:          payload,
	}
	return signBody(t, senderPrivKey, body, senderID)
}

func signBody(t *testing.T, senderPrivKey ed25519.PrivateKey, body *don2dontypes.MessageBody, senderID rage.PeerID) rage.Message {
	rawBody, err := proto.Marshal(body)
	require.NoError(t, err)
	signature := ed25519.Sign(senderPrivKey, rawBody)

	msg := don2dontypes.Message{
		Signature: signature,
		Body:      rawBody,
	}
	rawMsg, err := proto.Marshal(&msg)
	require.NoError(t, err)

	return rage.Message{
		Sender:  senderID,
		Payload: rawMsg,
	}
}

func TestToPeerID(t *testing.T) {
	t.Parallel()

	id, err := don2don.ToPeerID([]byte("12345678901234567890123456789012"))
	require.NoError(t, err)
	require.Equal(t, "12D3KooWD8QYTQVYjB6oog4Ej8PcPpqTrPRnxLQap8yY8KUQRVvq", id.String())
}

func TestSanitizeLogString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "hello", don2don.SanitizeLogString("hello"))
	require.Equal(t, "[UNPRINTABLE] 0a", don2don.SanitizeLogString("\n"))

	var longString strings.Builder
	for range 100 {
		longString.WriteString("aa-aa-aa-")
	}
	require.Equal(t, longString.String()[:256]+" [TRUNCATED]", don2don.SanitizeLogString(longString.String()))
}
