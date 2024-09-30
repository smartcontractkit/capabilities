package oracle

type Identity struct {
	PeerID                    string
	PublicKey                 []byte
	OffchainPublicKey         [32]byte
	ConfigEncryptionPublicKey [32]byte
}
