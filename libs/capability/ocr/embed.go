package ocr

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"strconv"

	"github.com/samber/do/v2"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/capabilities/libs/capability"
)

func newEmbeddedNetwork(i do.Injector) (Network, error) {
	info, err := do.Invoke[capability.InstanceInfo](i)
	if err != nil {
		return Network{}, err
	}

	return embeddedNetwork(embedNetwork, info.ID)
}

func embeddedNetwork(net *network, id int) (Network, error) {
	peerID, err := EmbeddedPeerID(id)
	if err != nil {
		return Network{}, err
	}

	return Network{BinaryNetworkEndpointFactory: ocr2Factory{net: net, peerID: peerID, bufferSize: defaultBufferSize}}, nil
}

func EmbeddedPeerID(id int) (string, error) {
	peerSeed := seed("peer", id)
	peerID, err := ragetypes.PeerIDFromPublicKey(ed25519.NewKeyFromSeed(peerSeed[:]).Public().(ed25519.PublicKey))
	if err != nil {
		return "", fmt.Errorf("failed to derive the peer ID of instance %d: %w", id, err)
	}

	return peerID.String(), nil
}

// seedPrefix separates the keys derived here from any other keys derived from an index.
const seedPrefix = "cre/capability/embed/ocr/"

func seed(purpose string, i int) [32]byte {
	return sha256.Sum256([]byte(seedPrefix + purpose + "/" + strconv.Itoa(i)))
}
