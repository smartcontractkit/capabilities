// Package keystore lends a process the node's keys: the node serves a gRPC service per keystore, all on one server,
// and the process signs through them. Private keys never leave the node. Each keystore has its own sub-package, such
// as ocr.
package keystore

import (
	"google.golang.org/grpc"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
	ocrremote "github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/remote"
)

// Keystores are the keystores the node lends a process. A nil field serves no such service.
type Keystores struct {
	OCR ocr.Keystore
}

// RegisterServers registers the service of each keystore in ks.
func RegisterServers(s grpc.ServiceRegistrar, ks Keystores) {
	if ks.OCR != nil {
		ocrremote.RegisterServer(s, ks.OCR)
	}
}
