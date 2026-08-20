package capability

import (
	"context"
	"errors"
	"sync"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// EmbeddedOCRConfig builds the registry an embedded run's oracles read their configuration from,
// given how many instances that run has.
type EmbeddedOCRConfig func(oracles int) core.OCRConfigRegistry

var embeddedOCRConfig struct {
	sync.Mutex
	build EmbeddedOCRConfig
}

// RegisterEmbeddedOCRConfig says where an embedded run's OCR configuration comes from. It is called
// from libs/standalone/ocr's init, and by nothing else.
//
// Why it exists at all: a configured capability reads its OCR configuration from the node's
// registry, which this dependency already talks to. An embedded one has no node, so the
// configuration has to be computed from the run itself - the instances, as their derived identities.
// Both have to arrive as the same interface on the same field, or an application could tell which
// kind of run it is, and not being able to tell is the whole point of ForEmbedding.
//
// Why a global rather than an argument: computing that configuration needs derived OCR key bundles,
// and those come from chainlink-common/keystore - one package with a keyring per chain family, so
// reaching for it drags go-ethereum, TON and starknet in behind it. Asking for it here would put all
// of that in the module graph of every capability binary, including ones that host a trigger and run
// no oracle at all. Registering from ocr's init instead means a binary pays for it exactly when it
// links the package that needs it - which an OCR-based capability already does, and a cron trigger
// never will. It is the bargain a database driver strikes with database/sql.
//
// Registering twice panics: two providers disagreeing about a DON is not something to resolve by
// letting the last one win.
//
// Call from an init function.
func RegisterEmbeddedOCRConfig(build EmbeddedOCRConfig) {
	embeddedOCRConfig.Lock()
	defer embeddedOCRConfig.Unlock()

	if embeddedOCRConfig.build != nil {
		panic("capability: an embedded OCR config registry is already registered")
	}
	embeddedOCRConfig.build = build
}

// embeddedOCRConfigRegistry is what an embedded run's capabilities read their OCR configuration
// from: the registered builder over this run's instances, or something that says why it cannot
// answer.
func embeddedOCRConfigRegistry(oracles int) core.OCRConfigRegistry {
	embeddedOCRConfig.Lock()
	build := embeddedOCRConfig.build
	embeddedOCRConfig.Unlock()

	if build == nil {
		return unregisteredOCRConfig{}
	}
	return build(oracles)
}

// unregisteredOCRConfig stands in when nothing registered a builder, which is the ordinary case for a
// capability that runs no oracle: it is handed a registry like any other, and only asking it a
// question it was never going to be able to answer fails.
type unregisteredOCRConfig struct{}

var _ core.OCRConfigRegistry = unregisteredOCRConfig{}

func (unregisteredOCRConfig) OCRConfig(context.Context, string, uint32, string) (ocrtypes.ContractConfig, error) {
	return ocrtypes.ContractConfig{}, errors.New(
		"this embedded run computes no OCR configuration: nothing registered one, which is what linking libs/standalone/ocr does")
}
