package node

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/smartcontractkit/capabilities/libs/cli/utils"
)

type P2PKeys struct {
	ID        string `json:"id"`
	PeerID    string `json:"peerId"`
	PublicKey string `json:"publicKey"`
}

func extractP2PKeys(text string) (*P2PKeys, error) {
	var id, peerID, publicKey string

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "ID:"):
			id = strings.TrimSpace(line[len("ID:"):])
		case strings.HasPrefix(line, "Peer ID:"):
			value := strings.TrimSpace(line[len("Peer ID:"):])
			peerID = extractKey(value)
		case strings.HasPrefix(line, "Public key:"):
			publicKey = strings.TrimSpace(line[len("Public key:"):])
		}
	}

	return &P2PKeys{
		ID:        id,
		PeerID:    peerID,
		PublicKey: publicKey,
	}, nil
}

func extractEthAddress(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Address:") {
			return strings.TrimSpace(line[len("Address:"):])
		}
	}

	return ""
}

func extractCSAKey(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Public key:") {
			value := strings.TrimSpace(line[len("Public key:"):])
			return extractKey(value)
		}
	}

	return ""
}

func extractKey(value string) string {
	parts := strings.Split(value, "_")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return value
}

type OCR2Keys struct {
	OCRKeyBundleID    string
	OCROnchainPubkey  string
	OCROffchainPubkey string
	OCRConfigPubkey   string
}

func extractOcrPubKeys(text string) (*OCR2Keys, error) {
	var ocrKeyBundleID, ocrOnchainPubkey, ocrOffchainPubkey, ocrConfigPubkey string

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "ID:"):
			ocrKeyBundleID = strings.TrimSpace(line[len("ID:"):])
		case strings.HasPrefix(line, "On-chain pubkey:"):
			value := strings.TrimSpace(line[len("On-chain pubkey:"):])
			ocrOnchainPubkey = extractKey(value)
		case strings.HasPrefix(line, "Off-chain pubkey:"):
			value := strings.TrimSpace(line[len("Off-chain pubkey:"):])
			ocrOffchainPubkey = extractKey(value)
		case strings.HasPrefix(line, "Config pubkey:"):
			value := strings.TrimSpace(line[len("Config pubkey:"):])
			ocrConfigPubkey = extractKey(value)
		}
	}

	return &OCR2Keys{
		OCRKeyBundleID:    ocrKeyBundleID,
		OCROnchainPubkey:  ocrOnchainPubkey,
		OCROffchainPubkey: ocrOffchainPubkey,
		OCRConfigPubkey:   ocrConfigPubkey,
	}, nil
}

type PublicKeys struct {
	EthAddress         string `json:"ethAddress"`
	P2PPeerID          string `json:"p2pPeerID"`
	OCR2BundleID       string `json:"ocr2BundleID"`
	OCR2OnchainPubkey  string `json:"ocr2OnchainPubkey"`
	OCR2OffchainPubkey string `json:"ocr2OffchainPubkey"`
	OCR2ConfigPubkey   string `json:"ocr2ConfigPubkey"`
	CSAPublicKey       string `json:"csaPublicKey"`
}

func GetPublicKeys(nodeID int) (*PublicKeys, error) {
	nodeInfo := utils.GetNodeInfo(nodeID)
	publicKeysBytes, err := os.ReadFile(nodeInfo.Paths.PublicKeys)
	if err != nil {
		return nil, err
	}

	var publicKeys PublicKeys
	if json.Unmarshal(publicKeysBytes, &publicKeys) != nil {
		return nil, fmt.Errorf("failed to unmarshal public keys")
	}

	return &publicKeys, nil
}

func FetchKeys(nodeIDs []int) error {
	for _, nodeID := range nodeIDs {
		err := Login(nodeID)
		if err != nil {
			return fmt.Errorf("failed to login to node %d: %w", nodeID, err)
		}

		err = fetchNodeKeys(nodeID)
		if err != nil {
			return fmt.Errorf("failed to fetch keys for node %d: %w", nodeID, err)
		}
	}

	return nil
}

func fetchNodeKeys(nodeID int) error {
	nodeInfo := utils.GetNodeInfo(nodeID)
	var err error

	fmt.Printf("Fetching keys for Node %d... ", nodeID)

	output, err := utils.ExecCommand(
		constants.ChainlinkBinaryLocation,
		"--remote-node-url", nodeInfo.URLs.HTTP,
		"--admin-credentials-file", nodeInfo.Paths.Credentials,
		"keys", "ocr2", "list",
	)
	if err != nil {
		return fmt.Errorf("failed to list OCR keys: %w", err)
	}

	ocrPubKeys, err := extractOcrPubKeys(string(output))
	if err != nil {
		return fmt.Errorf("failed to extract OCR keys: %w", err)
	}

	output, err = utils.ExecCommand(
		constants.ChainlinkBinaryLocation,
		"--remote-node-url", nodeInfo.URLs.HTTP,
		"--admin-credentials-file", nodeInfo.Paths.Credentials,
		"keys", "p2p", "list",
	)
	if err != nil {
		return fmt.Errorf("failed to list p2p keys: %w", err)
	}

	p2pKeys, err := extractP2PKeys(string(output))
	if err != nil {
		return fmt.Errorf("failed to extract OCR keys: %w", err)
	}

	ethOutput, err := utils.ExecCommand(
		constants.ChainlinkBinaryLocation,
		"--remote-node-url", nodeInfo.URLs.HTTP,
		"--admin-credentials-file", nodeInfo.Paths.Credentials,
		"keys", "eth", "list",
	)
	if err != nil {
		return fmt.Errorf("failed to list eth keys: %w", err)
	}

	csaOutput, err := utils.ExecCommand(
		constants.ChainlinkBinaryLocation,
		"--remote-node-url", nodeInfo.URLs.HTTP,
		"--admin-credentials-file", nodeInfo.Paths.Credentials,
		"keys", "csa", "list",
	)
	if err != nil {
		return fmt.Errorf("failed to list csa keys: %w", err)
	}

	publicKeys := PublicKeys{
		EthAddress:         extractEthAddress(string(ethOutput)),
		P2PPeerID:          p2pKeys.PeerID,
		OCR2BundleID:       ocrPubKeys.OCRKeyBundleID,
		OCR2OnchainPubkey:  ocrPubKeys.OCROnchainPubkey,
		OCR2OffchainPubkey: ocrPubKeys.OCROffchainPubkey,
		OCR2ConfigPubkey:   ocrPubKeys.OCRConfigPubkey,
		CSAPublicKey:       extractCSAKey(string(csaOutput)),
	}

	publicKeysJSON, err := json.MarshalIndent(publicKeys, "", "  ")
	if err != nil {
		return err
	}

	err = os.WriteFile(nodeInfo.Paths.PublicKeys, publicKeysJSON, 0600)
	if err != nil {
		return err
	}
	fmt.Printf("Done\n")

	return err
}
