// Package rage is the standalone.BootstrapDependency a binary uses to host the node's libocr rage
// peer: the real thing that listens, dials, announces and signs.
//
// It is separate from libs/standalone/ocr, which is what a binary driving an oracle over someone
// else's peer takes, because the two need different halves of the world and only one of them is
// expensive. Hosting a peer means unlocking the node's keystore, which holds a key of every kind the
// node has and brings every one of those chains' libraries with it. Delegating to a peer means
// verifying signatures and reading a configuration, and nothing else. Keeping them apart is what
// stops a binary that hosts a trigger from linking a cosmos SDK.
package rage

import (
	"time"

	"github.com/smartcontractkit/libocr/networking"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/ocrcommon"

	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

// Factories is ocr.Factories plus what only a real, locally hosted peer can give: a factory for
// DON-to-DON peer groups over that same rage connection, and the keyring to sign with the same key
// it uses. don2don.Dispatcher takes both.
type Factories struct {
	ocr.Factories

	// PeerGroup creates DON-to-DON peer groups.
	PeerGroup networking.PeerGroupFactory
	// Keyring signs with the same P2P key the peer above uses, for don2don.Dispatcher's
	// message-level signatures.
	Keyring ragetypes.PeerKeyring
}

// defaultPeerConfig is the peer settings the flags are bound to and decoded into, so an unset
// setting keeps the value it is given here. Freshly allocated per call rather than shared, so two
// dependencies (or two instances of one) can never decode into the same peer settings.
func defaultPeerConfig() *ocrcommon.Config {
	return &ocrcommon.Config{
		DeltaReconcile:     *config.MustNewDuration(time.Minute),
		DeltaDial:          *config.MustNewDuration(5 * time.Second),
		IncomingBufferSize: 100,
		OutgoingBufferSize: 100,
	}
}
