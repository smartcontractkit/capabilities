package main

// noopBlocksProvider - returns 0 for all blocks. Used when the chain does not have lockable to a block requests.
type noopBlocksProvider struct{}

func (n noopBlocksProvider) GetLatest() int64    { return 0 }
func (n noopBlocksProvider) GetSafe() int64      { return 0 }
func (n noopBlocksProvider) GetFinalized() int64 { return 0 }
