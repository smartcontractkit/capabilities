package rings

import (
	"strconv"

	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash/v2"
)

// RingMember implements consistent.Member for ring membership
type RingMember string

func (r RingMember) String() string {
	return string(r)
}

// hasher implements consistent.Hasher using xxhash
type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	return xxhash.Sum64(data)
}

// NewHasher creates a new hasher for consistent hashing
func NewHasher() consistent.Hasher {
	return hasher{}
}

// StableTable creates a consistent hash ring with the given number of members
func StableTable(count int, config consistent.Config) *consistent.Consistent {
	members := make([]consistent.Member, count)
	for i := 0; i < count; i++ {
		members[i] = RingMember(strconv.Itoa(i))
	}
	c := consistent.New(members, config)
	return c
}

// Route determines which ring a request should be routed to
func Route(table *consistent.Consistent, requestHash []byte) int {
	member := table.LocateKey(requestHash)
	if member == nil {
		return 0
	}
	ringID, _ := strconv.Atoi(member.String())
	return ringID
}
