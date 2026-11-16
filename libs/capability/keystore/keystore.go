// Package keystore lends a process the node's keys: the node serves a gRPC service per keystore, and the process
// signs through it. Private keys never leave the node.
package keystore

import (
	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// Keystores are the keystores the node lends a process. A nil field serves no such service.
type Keystores struct {
	OCR OCRKeystore
}

// RegisterServers registers the service of each keystore in ks.
func RegisterServers(s grpc.ServiceRegistrar, lggr logger.Logger, ks Keystores) {
	if ks.OCR != nil {
		RegisterOCRKeystoreServer(s, lggr, ks.OCR)
	}
}
