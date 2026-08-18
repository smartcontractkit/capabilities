// Command setup gives one crecore proxy a database to run against: a keystore holding a fresh P2P
// key, in the shape crecore expects to find a node's keystore in.
//
// crecore borrows its peer identity from the node whose database it shares, by decrypting the
// node's key ring (the legacy encrypted_key_rings table). There is no node here, so this writes
// that row itself. It prints the peer ID of the key it created and nothing else, so a script can
// capture it and tell the app which peer it is talking as.
//
// For local demos only. The key is generated here and encrypted with a password passed on the
// command line, which is fine for something whose whole purpose is to say hi to itself four times.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/smartcontractkit/chainlink-common/keystore"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/models"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/p2pkey"
	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil"
)

func main() {
	url := flag.String("url", "", "postgres url of the database this proxy will use")
	password := flag.String("password", "", "password the key ring is encrypted with; the proxy's --ocr.keystore-password")
	flag.Parse()

	if *url == "" || *password == "" {
		log.Fatal("both -url and -password are required")
	}

	peerID, err := setup(context.Background(), *url, *password)
	if err != nil {
		log.Fatal(err)
	}

	// The only thing on stdout, so `peer_id=$(setup ...)` works.
	fmt.Println(peerID)
}

// setup writes a key ring holding one new P2P key, replacing whatever was there, and returns the
// peer ID of that key.
func setup(ctx context.Context, url, password string) (string, error) {
	db, err := sqlutil.OpenDB(sqlutil.Config{URL: url})
	if err != nil {
		return "", fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	key, err := p2pkey.NewV2()
	if err != nil {
		return "", fmt.Errorf("failed to generate a P2P key: %w", err)
	}

	ring := models.NewKeyRing()
	ring.P2P[key.ID()] = key
	// Fast parameters: this is a throwaway key on a local machine, and the default ones take long
	// enough per instance to be irritating.
	encrypted, err := ring.Encrypt(password, keystore.FastScryptParams)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt the key ring: %w", err)
	}

	if err := writeKeyRing(ctx, db, encrypted.EncryptedKeys); err != nil {
		return "", err
	}
	return key.ID(), nil
}

// writeKeyRing creates the keystore table if this database has never held one - a node would have
// migrated it in - and leaves exactly one key ring in it.
func writeKeyRing(ctx context.Context, db *sql.DB, encryptedKeys []byte) error {
	const createTable = `CREATE TABLE IF NOT EXISTS encrypted_key_rings (
		encrypted_keys bytea NOT NULL,
		updated_at timestamptz NOT NULL
	)`
	if _, err := db.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("failed to create encrypted_key_rings: %w", err)
	}

	// Replaced rather than added to: crecore takes the first row it finds, so a second one would
	// make which peer it is a matter of luck.
	if _, err := db.ExecContext(ctx, `DELETE FROM encrypted_key_rings`); err != nil {
		return fmt.Errorf("failed to clear encrypted_key_rings: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO encrypted_key_rings (encrypted_keys, updated_at) VALUES ($1, $2)`,
		encryptedKeys, time.Now()); err != nil {
		return fmt.Errorf("failed to write the key ring: %w", err)
	}
	return nil
}
