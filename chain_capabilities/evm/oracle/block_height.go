package oracle

import (
	"fmt"
	"sort"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

func maxProtoBigInt(values ...*pb.BigInt) *pb.BigInt {
	if len(values) == 0 {
		return nil
	}

	highest := values[0]
	for _, value := range values[1:] {
		if cmpProtoBigInt(highest, value) < 0 {
			highest = value
		}
	}

	return highest
}

func cmpProtoBigInt(x, y *pb.BigInt) int {
	if x == nil {
		return -1
	} else if y == nil {
		return 1
	}

	return values.ProtoToBigInt(x).Cmp(values.ProtoToBigInt(y))
}

func ensureGtOrEq(aPb, bPb *pb.BigInt) error {
	a := values.ProtoToBigInt(aPb)
	b := values.ProtoToBigInt(bPb)
	if a.Cmp(b) < 0 {
		return fmt.Errorf("expected %s to be >= %s", a, b)
	}

	return nil
}

func validateBlockHeight(observation *evmservice.Observations) error {
	err := ensureGtOrEq(observation.Latest, observation.Safe)
	if err != nil {
		return fmt.Errorf("expected latest to be gtOrEq to safe: %w", err)
	}

	err = ensureGtOrEq(observation.Safe, observation.Finalized)
	if err != nil {
		return fmt.Errorf("expected safe to be gtOrEq to finalized: %w", err)
	}

	return nil
}

func fPlusOneLowestBlockHeight(obs []evmservice.Observations, f int, getHeight func(ob *evmservice.Observations) *pb.BigInt) (*pb.BigInt, error) {
	if len(obs) < f+1 {
		return nil, fmt.Errorf("not enough observations to calculate F+1 lowest block height. Got %d, expected at least %d", len(obs), f+1)
	}
	sort.Slice(obs, func(i, j int) bool {
		return cmpProtoBigInt(getHeight(&obs[i]), getHeight(&obs[j])) < 0
	})

	return getHeight(&obs[f]), nil
}

func validateBlockHeightAgainstOutcome(ob *evmservice.Observations, prev *evmservice.Outcome) error {
	err := ensureGtOrEq(ob.Latest, prev.Latest)
	if err != nil {
		return fmt.Errorf("expected latest to be gtOrEq to previous latest: %w", err)
	}

	err = ensureGtOrEq(ob.Safe, prev.Safe)
	if err != nil {
		return fmt.Errorf("expected safe to be gtOrEq to previous safe: %w", err)
	}

	err = ensureGtOrEq(ob.Finalized, prev.Finalized)
	if err != nil {
		return fmt.Errorf("expected finalized to be gtOrEq to previous finalized: %w", err)
	}

	return nil
}
