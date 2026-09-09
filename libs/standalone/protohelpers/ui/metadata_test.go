package ui

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocr3types "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
)

func TestHeaderSuffix(t *testing.T) {
	for field, want := range map[string]string{
		"WorkflowID":                    "WORKFLOW-ID",
		"OrgID":                         "ORG-ID",
		"WorkflowDonConfigVersion":      "WORKFLOW-DON-CONFIG-VERSION",
		"SpendLimits":                   "SPEND-LIMITS",
		"ExecutionTimestamp":            "EXECUTION-TIMESTAMP",
		"WorkflowRegistryChainSelector": "WORKFLOW-REGISTRY-CHAIN-SELECTOR",
	} {
		assert.Equal(t, want, headerSuffix(field), field)
	}
}

// Every field of RequestMetadata gets a header, so a field added upstream is
// carried without this package being touched.
func TestFieldsCoverEveryMetadataField(t *testing.T) {
	fields := Fields()
	require.NotEmpty(t, fields)

	byName := map[string]Field{}
	for _, f := range fields {
		byName[f.Name] = f
	}

	for _, name := range []string{
		"WorkflowID", "WorkflowOwner", "OrgID", "WorkflowExecutionID", "WorkflowName",
		"WorkflowDonID", "WorkflowDonConfigVersion", "ReferenceID", "DecodedWorkflowName",
		"SpendLimits", "WorkflowTag", "WorkflowRegistryChainSelector",
		"WorkflowRegistryAddress", "EngineVersion", "ExecutionTimestamp",
	} {
		f, ok := byName[name]
		require.True(t, ok, "%s has no header", name)
		assert.True(t, len(f.Header) > len(HeaderPrefix), "%s: %q", name, f.Header)
	}

	assert.Equal(t, KindNumber, byName["WorkflowDonID"].Kind)
	assert.Equal(t, KindText, byName["WorkflowID"].Kind)
	assert.Equal(t, KindTimestamp, byName["ExecutionTimestamp"].Kind)
	assert.True(t, byName["SpendLimits"].Repeated, "spend limits take one header per entry")
}

// Nothing specified means a request a capability will accept rather than one it
// rejects for a missing field.
func TestMetadataDefaultsWhenNothingIsSpecified(t *testing.T) {
	before := time.Now().UTC()
	md, err := MetadataFromHeaders(func(string) []string { return nil })
	require.NoError(t, err)

	assert.Equal(t, markedHex(workflowIDBytes), md.WorkflowID)
	assert.Equal(t, markedHex(workflowOwnerBytes), md.WorkflowOwner)
	assert.NotContains(t, md.WorkflowID, "test", "the defaults are the UI's, not a test's")
	assert.EqualValues(t, 1, md.WorkflowDonID)
	assert.EqualValues(t, 1, md.WorkflowDonConfigVersion)
	assert.NotEmpty(t, md.WorkflowExecutionID)

	// The timestamp is the one default that cannot be a constant.
	assert.False(t, md.ExecutionTimestamp.Before(before), "%s", md.ExecutionTimestamp)
}

func TestMetadataFromHeaders(t *testing.T) {
	header := http.Header{}
	header.Set(HeaderPrefix+"WORKFLOW-ID", strings.Repeat("cd", workflowIDBytes))
	header.Set(HeaderPrefix+"WORKFLOW-DON-ID", "7")
	header.Set(HeaderPrefix+"EXECUTION-TIMESTAMP", "2026-08-19T10:11:12Z")
	// Repeated is repeated headers, since a header can hold a list.
	header.Add(HeaderPrefix+"SPEND-LIMITS", "CONSENSUS=100000")
	header.Add(HeaderPrefix+"SPEND-LIMITS", "COMPUTE=5")

	md, err := MetadataFromHeaders(header.Values)
	require.NoError(t, err)

	assert.Equal(t, strings.Repeat("cd", workflowIDBytes), md.WorkflowID)
	assert.EqualValues(t, 7, md.WorkflowDonID)
	assert.Equal(t, "2026-08-19T10:11:12Z", md.ExecutionTimestamp.Format(time.RFC3339))
	assert.Equal(t, []capabilities.SpendLimit{
		{SpendType: "CONSENSUS", Limit: "100000"},
		{SpendType: "COMPUTE", Limit: "5"},
	}, md.SpendLimits)

	// Anything not sent still falls back.
	assert.Equal(t, markedHex(workflowOwnerBytes), md.WorkflowOwner)
	assert.EqualValues(t, 1, md.WorkflowDonConfigVersion)
}

// A blank header is the same as an absent one: the UI leaves empty boxes off the
// wire, and an empty value would otherwise override the default with nothing.
func TestBlankHeadersFallBackToDefaults(t *testing.T) {
	header := http.Header{}
	header.Set(HeaderPrefix+"WORKFLOW-ID", "   ")

	md, err := MetadataFromHeaders(header.Values)
	require.NoError(t, err)
	assert.Equal(t, markedHex(workflowIDBytes), md.WorkflowID)
}

// A value the caller asked for that cannot be parsed is an error, not a silent
// default: they would otherwise get neither their value nor a warning.
func TestUnparseableValuesAreErrors(t *testing.T) {
	for name, value := range map[string]string{
		"WORKFLOW-DON-ID":     "not-a-number",
		"EXECUTION-TIMESTAMP": "yesterday",
		"SPEND-LIMITS":        "no-equals-sign",
	} {
		header := http.Header{}
		header.Set(HeaderPrefix+name, value)

		_, err := MetadataFromHeaders(header.Values)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), name)
	}
}

func TestHeaderNamesArePrefixed(t *testing.T) {
	names := HeaderNames()
	require.Len(t, names, len(Fields()))
	for _, n := range names {
		assert.Contains(t, n, HeaderPrefix)
	}
}

// Two requests are two executions, so the identifier a capability might key work
// on cannot be shared between them.
func TestExecutionIDIsUniquePerRequest(t *testing.T) {
	first, err := MetadataFromHeaders(func(string) []string { return nil })
	require.NoError(t, err)
	second, err := MetadataFromHeaders(func(string) []string { return nil })
	require.NoError(t, err)

	assert.NotEqual(t, first.WorkflowExecutionID, second.WorkflowExecutionID)
	assert.True(t, strings.HasPrefix(first.WorkflowExecutionID, uiMarker))

	// The workflow itself is the same workflow, so that one is stable.
	assert.Equal(t, first.WorkflowID, second.WorkflowID)
}

// A caller who names an execution gets the one they named.
func TestExecutionIDCanBeSpecified(t *testing.T) {
	header := http.Header{}
	mine := strings.Repeat("ef", executionIDBytes)
	header.Set(HeaderPrefix+"WORKFLOW-EXECUTION-ID", mine)

	md, err := MetadataFromHeaders(header.Values)
	require.NoError(t, err)
	assert.Equal(t, mine, md.WorkflowExecutionID)
}

// Rendering metadata back to headers and reading it again returns what went in.
// This is what a fan-out relies on to send one metadata to several instances.
func TestHeadersRoundTrip(t *testing.T) {
	original, err := MetadataFromHeaders(func(string) []string { return nil })
	require.NoError(t, err)
	original.SpendLimits = []capabilities.SpendLimit{
		{SpendType: "CONSENSUS", Limit: "100000"},
		{SpendType: "COMPUTE", Limit: "5"},
	}

	header := HeadersFromMetadata(original)
	roundTripped, err := MetadataFromHeaders(header.Values)
	require.NoError(t, err)

	assert.Equal(t, original.WorkflowExecutionID, roundTripped.WorkflowExecutionID)
	assert.Equal(t, original.WorkflowID, roundTripped.WorkflowID)
	assert.Equal(t, original.WorkflowDonID, roundTripped.WorkflowDonID)
	assert.Equal(t, original.SpendLimits, roundTripped.SpendLimits)
	assert.Equal(t,
		original.ExecutionTimestamp.Format(time.RFC3339),
		roundTripped.ExecutionTimestamp.Format(time.RFC3339))
}

// A value that will not parse is the caller's to fix, so it is a 400 rather than
// the page reporting itself as broken.
func TestUnparseableValuesAreUserErrors(t *testing.T) {
	header := http.Header{}
	header.Set(HeaderPrefix+"WORKFLOW-DON-ID", "not-a-number")

	_, err := MetadataFromHeaders(header.Values)
	require.Error(t, err)
	assert.True(t, isUserError(err), "%v", err)
	assert.Equal(t, http.StatusBadRequest, httpStatus(err))
}

// The defaults have to satisfy the report encoder, not merely look plausible.
//
// This is the check that was missing. Values the encoder rejects do not fail the
// request: they fail the consensus round, so no report is produced, nothing is
// transmitted back, and the request expires. The user sees a timeout that says
// nothing about the field that caused it.
func TestDefaultsSatisfyTheReportEncoder(t *testing.T) {
	md, err := MetadataFromHeaders(func(string) []string { return nil })
	require.NoError(t, err)

	encoded, err := ocr3types.Metadata{
		Version:          1,
		ExecutionID:      md.WorkflowExecutionID,
		Timestamp:        uint32(md.ExecutionTimestamp.Unix()),
		DONID:            md.WorkflowDonID,
		DONConfigVersion: md.WorkflowDonConfigVersion,
		WorkflowID:       md.WorkflowID,
		WorkflowName:     md.WorkflowName,
		WorkflowOwner:    md.WorkflowOwner,
		ReportID:         "0000",
	}.Encode()
	require.NoError(t, err, "the defaults must encode: a value the encoder rejects surfaces as a timeout")
	assert.NotEmpty(t, encoded)
}

// The hex fields are the exact byte lengths the encoder checks.
func TestHexDefaultsHaveTheRequiredLengths(t *testing.T) {
	md, err := MetadataFromHeaders(func(string) []string { return nil })
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		value string
		bytes int
	}{
		"WorkflowExecutionID": {md.WorkflowExecutionID, executionIDBytes},
		"WorkflowID":          {md.WorkflowID, workflowIDBytes},
		"WorkflowName":        {md.WorkflowName, workflowNameBytes},
		"WorkflowOwner":       {md.WorkflowOwner, workflowOwnerBytes},
	} {
		t.Run(name, func(t *testing.T) {
			decoded, err := hex.DecodeString(tc.value)
			require.NoError(t, err, "%s must be hex, got %q", name, tc.value)
			assert.Len(t, decoded, tc.bytes)
			// Still says where it came from, despite having to be hex.
			assert.True(t, strings.HasPrefix(tc.value, uiMarker), "%s: %q", name, tc.value)
		})
	}
}

// A value the encoder would refuse is reported as the field it is, immediately,
// rather than becoming a consensus timeout twenty seconds later.
func TestHexFieldsAreValidatedUpFront(t *testing.T) {
	for name, value := range map[string]string{
		"WORKFLOW-ID":           "not-hex",
		"WORKFLOW-EXECUTION-ID": "also-not-hex",
		"WORKFLOW-OWNER":        "0xzz",
	} {
		t.Run(name, func(t *testing.T) {
			header := http.Header{}
			header.Set(HeaderPrefix+name, value)

			_, err := MetadataFromHeaders(header.Values)
			require.Error(t, err)
			assert.True(t, isUserError(err), "%v", err)
			assert.Contains(t, err.Error(), name, "the message should name the field")
		})
	}

	t.Run("the right length is required", func(t *testing.T) {
		header := http.Header{}
		header.Set(HeaderPrefix+"WORKFLOW-ID", "abcd") // hex, but 2 bytes not 32

		_, err := MetadataFromHeaders(header.Values)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "32 hex bytes")
	})
}
