package ocr

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"

	keystoreocr "github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
)

// identity is who the node's oracle is in the DON: its keys and transmit account, and the OCR config that lists them.
type identity struct {
	OffchainKeyring ocrtypes.OffchainKeyring
	// onchainKeyring signs with the key bundle a report's info names, such as evm. OnchainKeyring is it for a
	// capability's report info.
	onchainKeyring ocr3types.OnchainKeyring[string]
	// TransmitAccount is the account the config lists the node under.
	TransmitAccount        ocrtypes.Account
	ContractConfigTracker  ocrtypes.ContractConfigTracker
	OffchainConfigDigester ocrtypes.OffchainConfigDigester
}

// fetchTimeout is how long the node has to give its key bundles and the capability's OCR config.
const fetchTimeout = time.Minute

// nodeIdentity finds the node's keys and transmit account in the capability's OCR config, as the node's own oracles
// do: the node's key bundles that make up one of its signers, and the transmitter paired with it.
func nodeIdentity(ctx context.Context, keystore keystoreocr.Client, config *registryConfig) (identity, error) {
	cc, err := config.latest(ctx)
	if err != nil {
		return identity{}, err
	}

	bundles, err := keystore.ListKeyBundles(ctx)
	if err != nil {
		return identity{}, err
	}

	for j, signer := range cc.Signers {
		onchain, offchain, ok := matchSigner(bundles, signer)
		if ok && j < len(cc.Transmitters) {
			return identity{
				OffchainKeyring:        offchain,
				onchainKeyring:         onchain,
				TransmitAccount:        cc.Transmitters[j],
				ContractConfigTracker:  config,
				OffchainConfigDigester: config,
			}, nil
		}
	}

	return identity{}, fmt.Errorf("none of the node's %d OCR key bundles make up one of the %d signers in the OCR "+
		"config, with an evm bundle among them for the offchain keyring", len(bundles), len(cc.Signers))
}

// matchSigner is the keyring of the bundles that make up signer, a multichain key with one key per chain type, and
// the offchain keyring of its evm bundle.
func matchSigner(bundles []keystoreocr.KeyBundleClient, signer ocrtypes.OnchainPublicKey) (
	ocr3types.OnchainKeyring[string], ocrtypes.OffchainKeyring, bool) {
	var keys ocr2key.MultichainPublicKey
	if err := keys.UnmarshalBinary(signer); err != nil || len(keys) == 0 {
		return nil, nil, false
	}

	matched := make([]keystoreocr.KeyBundleClient, 0, len(keys))
	for chainType, key := range keys {
		j := slices.IndexFunc(bundles, func(b keystoreocr.KeyBundleClient) bool {
			return b.ChainType == chainType && bytes.Equal(b.Onchain.PublicKey(), key)
		})
		if j < 0 {
			return nil, nil, false
		}

		matched = append(matched, bundles[j])
	}

	evm := slices.IndexFunc(matched, func(b keystoreocr.KeyBundleClient) bool { return b.ChainType == string(corekeys.EVM) })
	if evm < 0 {
		return nil, nil, false
	}

	keyring, err := keystoreocr.NewMultichainOCR3OnchainKeyring(matched)
	// Unmarshalling skips chain types it doesn't know, so the matched bundles must make up exactly the signer.
	if err != nil || !bytes.Equal(keyring.PublicKey(), signer) {
		return nil, nil, false
	}

	return keyring, matched[evm].Offchain, true
}
