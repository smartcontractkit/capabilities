package rings

import (
	"testing"

	"github.com/buraksezer/consistent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRingMember_String(t *testing.T) {
	member := RingMember("test-ring-0")
	assert.Equal(t, "test-ring-0", member.String())
}

func TestHasher_Sum64(t *testing.T) {
	hasher := NewHasher()

	data1 := []byte("test-data-1")
	data2 := []byte("test-data-2")

	hash1 := hasher.Sum64(data1)
	hash2 := hasher.Sum64(data2)

	// Different data should produce different hashes
	assert.NotEqual(t, hash1, hash2)

	// Same data should produce same hash
	hash1Again := hasher.Sum64(data1)
	assert.Equal(t, hash1, hash1Again)
}

func TestStableTable(t *testing.T) {
	config := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            NewHasher(),
	}

	table := StableTable(3, config)
	require.NotNil(t, table)

	// Verify the table has the correct members
	members := table.GetMembers()
	require.Len(t, members, 3)
}

func TestRoute(t *testing.T) {
	config := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            NewHasher(),
	}

	table := StableTable(5, config)

	// Test routing different hashes
	hash1 := []byte("request-hash-1")
	hash2 := []byte("request-hash-2")
	hash3 := []byte("request-hash-1") // Same as hash1

	ring1 := Route(table, hash1)
	ring2 := Route(table, hash2)
	ring3 := Route(table, hash3)

	// Same hash should route to same ring
	assert.Equal(t, ring1, ring3)

	// All rings should be in valid range
	assert.GreaterOrEqual(t, ring1, 0)
	assert.Less(t, ring1, 5)
	assert.GreaterOrEqual(t, ring2, 0)
	assert.Less(t, ring2, 5)
}

func TestRoute_ConsistentHashing(t *testing.T) {
	config := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            NewHasher(),
	}

	// Create multiple requests and verify distribution
	table := StableTable(3, config)

	ringCounts := make(map[int]int)
	for i := 0; i < 100; i++ {
		hash := []byte{byte(i)}
		ring := Route(table, hash)
		ringCounts[ring]++
	}

	// Verify all rings got some requests (probabilistic test)
	// With 100 requests and 3 rings, each should get roughly 33 requests
	// We'll check that each got at least 10 (allowing for variance)
	for ring, count := range ringCounts {
		assert.Greater(t, count, 10, "Ring %d got too few requests: %d", ring, count)
	}
}
