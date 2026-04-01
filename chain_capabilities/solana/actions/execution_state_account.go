package actions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
)

// Anchor account discriminator for keystone_forwarder::state::ExecutionState (first 8 bytes of
// sha256("account:ExecutionState")). Must match the deployed forwarder program.
var executionStateAccountDiscriminator = [8]byte{31, 209, 35, 133, 132, 142, 151, 100}

// parseExecutionStateAccount decodes an ExecutionState account: 8-byte Anchor discriminator
// followed by Borsh: Pubkey transmitter, [32]byte transmission_id, bool success.
func parseExecutionStateAccount(data []byte) (transmitter solana.PublicKey, transmissionID [32]byte, success bool, err error) {
	const (
		discLen = 8
		minLen  = discLen + 32 + 32 + 1 // + transmitter + transmission_id + success
	)
	if len(data) < minLen {
		return solana.PublicKey{}, [32]byte{}, false, fmt.Errorf("execution state account data too short: %d", len(data))
	}
	if !bytes.Equal(data[:discLen], executionStateAccountDiscriminator[:]) {
		return solana.PublicKey{}, [32]byte{}, false, fmt.Errorf("unexpected ExecutionState account discriminator")
	}

	dec := bin.NewBorshDecoder(data[discLen:])
	if err = dec.Decode(&transmitter); err != nil {
		return solana.PublicKey{}, [32]byte{}, false, fmt.Errorf("decode transmitter: %w", err)
	}
	if err = dec.Decode(&transmissionID); err != nil {
		return solana.PublicKey{}, [32]byte{}, false, fmt.Errorf("decode transmission_id: %w", err)
	}
	if err = dec.Decode(&success); err != nil {
		return solana.PublicKey{}, [32]byte{}, false, fmt.Errorf("decode success: %w", err)
	}
	return transmitter, transmissionID, success, nil
}

// accountDataBytesFromJSON extracts raw account bytes from getAccountInfo jsonParsed / JSON payloads.
// Unknown programs use Solana's ["<base64>","base64"] shape; see JSON RPC spec for getAccountInfo.
func accountDataBytesFromJSON(asJSON []byte) ([]byte, error) {
	if len(asJSON) == 0 {
		return nil, fmt.Errorf("empty account data json")
	}
	var arr []string
	if err := json.Unmarshal(asJSON, &arr); err == nil && len(arr) >= 2 && arr[1] == "base64" {
		return base64.StdEncoding.DecodeString(arr[0])
	}
	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(asJSON, &wrapped); err == nil && len(wrapped.Data) > 0 {
		return accountDataBytesFromJSON(wrapped.Data)
	}
	return nil, fmt.Errorf("could not extract base64 account data from json")
}
