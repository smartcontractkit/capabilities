package oracle

import (
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/values"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func Test_handleCommonPrefixAggregation(t *testing.T) {
	type testCase struct {
		name       string
		giveValues []*valuespb.Value
		f          int
		wantValue  *valuespb.Value
		wantErr    string
	}

	testCases := []testCase{
		{
			name: "OK - common prefix of f+1 lists",
			giveValues: []*valuespb.Value{
				mustNewList("1", "2", "3", "4"),
				mustNewList("1", "2", "3", "5"),
				mustNewList("1", "2", "3", "6"),
				mustNewList("1", "2", "3", "7"),
			},
			f:         3,
			wantValue: mustNewList("1", "2", "3"),
		},
		{
			name: "OK - insufficient lists with common prefix returns empty list",
			giveValues: []*valuespb.Value{
				mustNewList("1", "2", "3", "4"),
				mustNewList("1", "2", "3", "5"),
				mustNewList("0", "1", "2", "5"),
				mustNewList("1", "2", "3", "7"),
			},
			f:         3,
			wantValue: values.Proto(&values.List{}),
		},
		{
			name:       "OK - no lists provided returns empty list",
			giveValues: []*valuespb.Value{},
			f:          3,
			wantValue:  values.Proto(&values.List{}),
		},
		{
			name: "OK - empty lists provided returns empty list",
			giveValues: []*valuespb.Value{
				mustNewList(),
				mustNewList(),
				mustNewList(),
			},
			f:         3,
			wantValue: values.Proto(&values.List{}),
		},
		{
			name: "NOK - non-list provided errors",
			giveValues: []*valuespb.Value{
				mustNewList(),
				mustNewList(),
				values.Proto(values.NewString("bad entry")),
			},
			f:       3,
			wantErr: "is not a list",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := handleCommonPrefixAggregation(tc.giveValues, tc.f)

			if tc.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			require.True(t, proto.Equal(tc.wantValue, got), "expected %v, got %v", tc.wantValue, got)
		})
	}
}

func mustNewList(elems ...any) *valuespb.Value {
	l, err := values.NewList(elems)
	if err != nil {
		panic(err)
	}
	return values.Proto(l)
}
