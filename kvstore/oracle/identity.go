package oracle

type Identity struct {
	EVMKey                    string   `json:"evm_key"`
	PeerID                    string   `json:"peer_id"`
	PublicKey                 []byte   `json:"public_key"`
	OffchainPublicKey         [32]byte `json:"offchain_public_key"`
	ConfigEncryptionPublicKey [32]byte `json:"config_encryption_public_key"`
}
