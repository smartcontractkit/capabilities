package utils

import (
	"fmt"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/require"
)

func FundAccounts(t *testing.T, accounts []solana.PrivateKey, solanaGoClient *rpc.Client) {
	fundAccounts(t, accounts, solanaGoClient, waitAndRetryOpts{
		RemainingAttempts: 5,
		Timeout:           30 * time.Second,
		Timestep:          500 * time.Millisecond,
	})
}

type waitAndRetryOpts struct {
	RemainingAttempts uint
	Timeout           time.Duration
	Timestep          time.Duration
}

func (o waitAndRetryOpts) WithDecreasedAttempts() waitAndRetryOpts {
	return waitAndRetryOpts{
		RemainingAttempts: o.RemainingAttempts - 1,
		Timeout:           o.Timeout,
		Timestep:          o.Timestep,
	}
}

func fundAccounts(t *testing.T, accounts []solana.PrivateKey, solanaGoClient *rpc.Client, opts waitAndRetryOpts) {
	ctx := t.Context()
	sigs := []solana.Signature{}
	for _, v := range accounts {
		sig, err := solanaGoClient.RequestAirdrop(ctx, v.PublicKey(), 1000*solana.LAMPORTS_PER_SOL, rpc.CommitmentFinalized)
		require.NoError(t, err)
		sigs = append(sigs, sig)
	}

	// wait for confirmation so later transactions don't fail
	remaining := accounts
	initTime := time.Now()
	for elapsed := time.Since(initTime); elapsed < opts.Timeout; elapsed = time.Since(initTime) {
		time.Sleep(opts.Timestep)

		statusRes, sigErr := solanaGoClient.GetSignatureStatuses(ctx, true, sigs...)
		require.NoError(t, sigErr)
		require.NotNil(t, statusRes)
		require.NotNil(t, statusRes.Value)

		accountsWithNonFinalizedFunding := []solana.PrivateKey{}
		for i, res := range statusRes.Value {
			if res == nil || res.ConfirmationStatus == rpc.ConfirmationStatusProcessed || res.ConfirmationStatus == rpc.ConfirmationStatusConfirmed {
				accountsWithNonFinalizedFunding = append(accountsWithNonFinalizedFunding, accounts[i])
			}
		}
		remaining = accountsWithNonFinalizedFunding

		if len(remaining) == 0 {
			return // all done!
		}
	}

	decreasedOpts := opts.WithDecreasedAttempts()
	if decreasedOpts.RemainingAttempts == 0 {
		require.NoError(t, fmt.Errorf("[%s]: unable to find transactions after all attempts", t.Name()))
	} else {
		fundAccounts(t, remaining, solanaGoClient, decreasedOpts) // recursive call with only remaining & with fewer attempts
	}
}
