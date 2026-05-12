package actions

import (
	"bytes"
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
