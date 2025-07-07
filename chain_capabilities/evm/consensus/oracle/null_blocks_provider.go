package oracle

var _ BlocksProvider = &NullBlocksProvider{}

// TODO PLEX-1560: replace with actual implementation
type NullBlocksProvider struct{}

func (n NullBlocksProvider) GetLatest() (int64, error) {
	return 0, nil
}

func (n NullBlocksProvider) GetSafe() (int64, error) {
	return 0, nil
}

func (n NullBlocksProvider) GetFinalized() (int64, error) {
	return 0, nil
}
