package ocr

import (
	"github.com/samber/do/v2"

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
	peerID, err := capability.EmbeddedPeerID(id)
	if err != nil {
		return Network{}, err
	}

	return Network{BinaryNetworkEndpointFactory: ocr2Factory{net: net, peerID: peerID.String(), bufferSize: defaultBufferSize}}, nil
}
