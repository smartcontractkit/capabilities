package pb_test

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/pb"
)

func TestConvertFilterFromProto(t *testing.T) {
	var validBlockHash []byte
	for i := 0; i < 32; i++ {
		validBlockHash = append(validBlockHash, byte(i))
	}

	var validAddress []byte
	for i := 0; i < 20; i++ {
		validAddress = append(validAddress, byte(i+11))
	}

	var validTopic []byte
	for i := 0; i < 32; i++ {
		validTopic = append(validTopic, byte(i+21))
	}

	t.Run("nil protoFilter returns error", func(t *testing.T) {
		_, err := pb.ConvertFilterFromProto(nil)
		assert.ErrorContains(t, err, "filter can't be nil")
	})

	t.Run("successful conversion", func(t *testing.T) {
		fromBlock := &valuespb.BigInt{AbsVal: []byte{1, 2, 3}, Sign: 0}
		toBlock := &valuespb.BigInt{AbsVal: []byte{1, 2, 4}, Sign: 0}
		validTopics := []*pb.Topics{{Topic: [][]byte{validTopic}}}
		input := &pb.FilterQuery{
			BlockHash: validBlockHash,
			FromBlock: fromBlock,
			ToBlock:   toBlock,
			Addresses: [][]byte{validAddress},
			Topics:    validTopics,
		}

		// expected outputs from conversions
		expectedHash := common.Hash(validBlockHash)
		expectedAddr := common.Address(validAddress)
		expectedTopics := pb.ConvertTopicsFromProto(validTopics)

		result, err := pb.ConvertFilterFromProto(input)
		assert.NoError(t, err)

		assert.ElementsMatch(t, expectedHash, result.BlockHash)
		assert.Equal(t, valuespb.NewIntFromBigInt(fromBlock), result.FromBlock)
		assert.Equal(t, valuespb.NewIntFromBigInt(toBlock), result.ToBlock)
		assert.ElementsMatch(t, [][20]byte{expectedAddr}, result.Addresses)
		assert.ElementsMatch(t, expectedTopics, result.Topics)
	})
}
