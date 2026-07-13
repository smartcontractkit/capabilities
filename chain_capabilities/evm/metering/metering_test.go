package metering

import (
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/stretchr/testify/assert"
)

func TestGetResponseMetadata(t *testing.T) {
	tests := []struct {
		name     string
		action   SpendValueCredits
		expected capabilities.ResponseMetadata
	}{
		{
			name:   "CallContract action",
			action: CallContract,
			expected: capabilities.ResponseMetadata{
				Metering: []capabilities.MeteringNodeDetail{
					{
						SpendValue: "2.5",
						SpendUnit:  ActionSpendUnit,
					},
				},
			},
		},
		{
			name:   "BalanceAt valid action",
			action: BalanceAt,
			expected: capabilities.ResponseMetadata{
				Metering: []capabilities.MeteringNodeDetail{
					{
						SpendValue: "1",
						SpendUnit:  ActionSpendUnit,
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetResponseMetadata(test.action)
			assert.Equal(t, test.expected, result)
			assert.Len(t, result.Metering, 1)
			assert.Empty(t, result.Metering[0].Peer2PeerID, "Peer2PeerID should be empty")
		})
	}
}

func TestCompareStringNumbers(t *testing.T) {
	tests := []struct {
		name           string
		limitStr       string
		actionSpendStr string
		expected       bool
		expectError    string
	}{
		{
			name:           "limit greater than spend",
			limitStr:       "10",
			actionSpendStr: "5",
			expected:       true,
			expectError:    "",
		},
		{
			name:           "spend greater than limit",
			limitStr:       "5",
			actionSpendStr: "10",
			expected:       false,
			expectError:    "",
		},
		{
			name:           "limit equals spend",
			limitStr:       "10",
			actionSpendStr: "10",
			expected:       true,
			expectError:    "",
		},
		{
			name:           "large equal numbers",
			limitStr:       "100000",
			actionSpendStr: "100000",
			expected:       true,
			expectError:    "",
		},
		{
			name:           "underscored numbers",
			limitStr:       "100_000",
			actionSpendStr: "100_000",
			expected:       true,
			expectError:    "",
		},
		{
			name:           "invalid limit",
			limitStr:       "invalid",
			actionSpendStr: "5",
			expected:       false,
			expectError:    "invalid number limit: strconv.ParseFloat: parsing \"invalid\": invalid syntax",
		},
		{
			name:           "invalid spend",
			limitStr:       "10",
			actionSpendStr: "invalid",
			expected:       false,
			expectError:    "invalid number actionSpend: strconv.ParseFloat: parsing \"invalid\": invalid syntax",
		},
		{
			// Write-report fee from fee.Text('f', -1) can be one wei above a round ETH limit.
			name:           "write report gas fee one wei over limit",
			limitStr:       "0.021",
			actionSpendStr: "0.021000000000000001",
			expected:       false,
			expectError:    "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := compareStringNumbers(test.limitStr, test.actionSpendStr)
			if test.expectError != "" {
				assert.Error(t, err)
				assert.ErrorContains(t, err, test.expectError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, result)
			}
		})
	}
}

func TestCheckHasFunds(t *testing.T) {
	tests := []struct {
		name        string
		meta        capabilities.RequestMetadata
		unit        string
		actionSpend string
		expectError string
	}{
		{
			name: "sufficient funds with valid unit",
			meta: capabilities.RequestMetadata{
				SpendLimits: []capabilities.SpendLimit{
					{SpendType: ActionSpendUnit, Limit: "10"},
				},
			},
			unit:        ActionSpendUnit,
			actionSpend: "5",
			expectError: "",
		},
		{
			name: "insufficient funds with valid unit",
			meta: capabilities.RequestMetadata{
				SpendLimits: []capabilities.SpendLimit{
					{SpendType: ActionSpendUnit, Limit: "5"},
				},
			},
			unit:        ActionSpendUnit,
			actionSpend: "10",
			expectError: "insufficient CRE funds: current limit is 5, action spend 10",
		},
		{
			name: "invalid unit",
			meta: capabilities.RequestMetadata{
				SpendLimits: []capabilities.SpendLimit{
					{SpendType: ActionSpendUnit, Limit: "10"},
				},
			},
			unit:        "INVALID_UNIT",
			actionSpend: "5",
			expectError: "", // empty limit is ignored, request allowed
		},
		{
			name: "invalid limit value",
			meta: capabilities.RequestMetadata{
				SpendLimits: []capabilities.SpendLimit{
					{SpendType: ActionSpendUnit, Limit: "invalid"},
				},
			},
			unit:        ActionSpendUnit,
			actionSpend: "5",
			expectError: "invalid number limit: strconv.ParseFloat: parsing \"invalid\": invalid syntax",
		},
		{
			name: "invalid action spend value",
			meta: capabilities.RequestMetadata{
				SpendLimits: []capabilities.SpendLimit{
					{SpendType: ActionSpendUnit, Limit: "10"},
				},
			},
			unit:        ActionSpendUnit,
			actionSpend: "invalid",
			expectError: "invalid number actionSpend: strconv.ParseFloat: parsing \"invalid\": invalid syntax",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lggr := logger.Sugared(logger.Test(t))
			err := CheckHasFunds(lggr, test.meta, test.unit, test.actionSpend)
			if test.expectError != "" {
				assert.Error(t, err)
				assert.ErrorContains(t, err, test.expectError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
