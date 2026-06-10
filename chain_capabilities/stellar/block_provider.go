package main

// noopBlocksProvider returns 0 for all blocks. Stellar consensus reads are volatile (keyed by
// ledger sequence inside the observation) and are never locked to a block height, so the OCR
// reporting plugin does not need a real chain-height provider.
type noopBlocksProvider struct{}

func (n noopBlocksProvider) GetLatest() int64    { return 0 }
func (n noopBlocksProvider) GetSafe() int64      { return 0 }
func (n noopBlocksProvider) GetFinalized() int64 { return 0 }
