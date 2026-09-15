package ui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// HeaderPrefix is what a RequestMetadata field is carried under. One header per
// field, named after the field, so nothing here has to be kept in step with the
// struct by hand: the list below is read off the type.
const HeaderPrefix = "X-CRE-REQUEST-METADATA-"

// Field is one RequestMetadata field, as the UI needs to render it and as the
// wire carries it.
type Field struct {
	// Name is the Go field name, which is what MetadataFromHeaders assigns to.
	Name string `json:"name"`
	// Header is the HTTP header (and gRPC metadata key) this field travels in.
	Header string `json:"header"`
	// Kind says which input to draw. It is derived from the Go type, so a field
	// gets the same box the proto would give it: text for a string, a number for
	// a uint32, a datetime for a timestamp.
	Kind string `json:"kind"`
	// Repeated fields take one header per entry rather than one header holding
	// them all, since http.Header and metadata.MD are both map[string][]string.
	Repeated bool `json:"repeated"`
	// Default is what an unspecified field is filled in with, shown so the UI can
	// pre-populate the box with the value that would be sent anyway.
	Default string `json:"default"`
}

// Kinds a Field can have. Anything unrecognised is text: a box the user can type
// into is a worse fit than a number box, but it is never wrong.
const (
	KindText      = "text"
	KindNumber    = "number"
	KindTimestamp = "timestamp"
	KindPair      = "pair"
)

// Fields is every RequestMetadata field, in declaration order.
//
// Built by reflection rather than listed, so a field added to RequestMetadata
// upstream shows up here - and in the UI, and on the wire - without this package
// being touched.
func Fields() []Field {
	t := reflect.TypeFor[capabilities.RequestMetadata]()
	defaults := defaultMetadata()
	value := reflect.ValueOf(defaults)

	fields := make([]Field, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		kind, repeated := kindOf(f.Type)
		fields = append(fields, Field{
			Name:     f.Name,
			Header:   HeaderPrefix + headerSuffix(f.Name),
			Kind:     kind,
			Repeated: repeated,
			Default:  formatDefault(value.Field(i)),
		})
	}
	return fields
}

// kindOf maps a Go type to the input the UI draws for it.
func kindOf(t reflect.Type) (kind string, repeated bool) {
	if t == reflect.TypeFor[time.Time]() {
		return KindTimestamp, false
	}
	switch t.Kind() {
	case reflect.Slice:
		// A slice of two-string tuples (SpendLimit) is a pair per entry.
		inner, _ := kindOf(t.Elem())
		if inner == KindText && t.Elem().Kind() == reflect.Struct {
			return KindPair, true
		}
		return inner, true
	case reflect.Struct:
		// SpendLimit is spend type plus limit, both strings, so one box each.
		return KindText, false
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return KindNumber, false
	default:
		return KindText, false
	}
}

// headerSuffix turns a Go field name into a header suffix: WorkflowID becomes
// WORKFLOW-ID, and WorkflowDonConfigVersion becomes WORKFLOW-DON-CONFIG-VERSION.
//
// Word boundaries are kept because HTTP header names are case-insensitive, so
// camel case alone would not survive canonicalisation - Workflowid and
// WorkflowID are the same header, and neither reads as the field it came from.
func headerSuffix(field string) string {
	var words []string
	runes := []rune(field)
	start := 0
	for i := 1; i <= len(runes); i++ {
		if i == len(runes) {
			words = append(words, string(runes[start:i]))
			break
		}
		prev, cur := runes[i-1], runes[i]
		upper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
		switch {
		case !upper(prev) && upper(cur):
			// camelC -> new word
			words = append(words, string(runes[start:i]))
			start = i
		case upper(prev) && upper(cur) && i+1 < len(runes) && !upper(runes[i+1]):
			// end of an acronym run: IDValue -> ID, Value
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	for i, w := range words {
		words[i] = strings.ToUpper(w)
	}
	return strings.Join(words, "-")
}

// Fields the report encoder requires as fixed-length hex. A consensus report
// carries the request metadata on-chain, so these are not free-form strings: the
// encoder decodes them and checks the byte count (see chainlink-common's
// consensus/ocr3/types.Metadata.Encode).
//
// Getting this wrong does not fail the request - it fails the round. The plugin
// cannot encode the metadata, so no report is produced, nothing is transmitted
// back, and the request the user made sits until it expires. What they see is a
// timeout, which says nothing about the value that caused it.
const (
	executionIDBytes   = 32
	workflowIDBytes    = 32
	workflowNameBytes  = 10
	workflowOwnerBytes = 20
)

// uiMarker is "ui" in ASCII. A value that has to be hex can still say where it
// came from, so a capability's logs name this page rather than showing an
// anonymous run of zeros.
const uiMarker = "7569"

// defaultMetadata is what a request gets for anything the caller left out.
//
// Every hex field is a valid one of the right length, because "valid" here means
// what the report encoder accepts rather than what looks reasonable.
//
// The execution identifier is not a constant. A capability may well key work,
// dedupe or cache on the execution it was asked under, so two requests sharing one
// would be two runs of the same execution rather than two executions. Every call
// therefore gets its own, and a fan-out settles on one before sending so its
// instances still agree (see HeadersFromMetadata).
func defaultMetadata() capabilities.RequestMetadata {
	return capabilities.RequestMetadata{
		WorkflowID:                    markedHex(workflowIDBytes),
		WorkflowOwner:                 markedHex(workflowOwnerBytes),
		OrgID:                         "ui-org-id",
		WorkflowExecutionID:           uniqueHex(executionIDBytes),
		WorkflowName:                  markedHex(workflowNameBytes),
		WorkflowDonID:                 1,
		WorkflowDonConfigVersion:      1,
		ReferenceID:                   "ui-reference-id",
		DecodedWorkflowName:           "ui-workflow-name",
		WorkflowTag:                   "ui-workflow-tag",
		WorkflowRegistryChainSelector: "ui-chain-selector",
		WorkflowRegistryAddress:       markedHex(workflowOwnerBytes),
		EngineVersion:                 "v1",
		ExecutionTimestamp:            time.Now().UTC(),
	}
}

// markedHex is a stable hex value of exactly n bytes, opening with the marker so
// it is recognisable in a log.
func markedHex(n int) string {
	return pad(uiMarker, n)
}

// uniqueHex is a per-request hex value of exactly n bytes: the marker, then
// randomness. Random rather than a counter, so two processes - the instances of an
// embed run, say - cannot mint the same one.
func uniqueHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// Randomness is unavailable, which is not worth failing a debug request
		// over. A timestamp still separates this from the request before it.
		return pad(uiMarker+strconv.FormatInt(time.Now().UnixNano(), 16), n)
	}
	return pad(uiMarker+hex.EncodeToString(buf), n)
}

// pad trims or zero-fills a hex string to exactly n bytes, which is the length the
// encoder checks.
func pad(value string, n int) string {
	width := n * 2
	if len(value) >= width {
		return value[:width]
	}
	return value + strings.Repeat("0", width-len(value))
}

// HeadersFromMetadata renders metadata back into the headers it travels in.
//
// This is what lets a fan-out send one metadata to every instance: it resolves the
// defaults once, then sends the result explicitly rather than letting each
// instance fill in its own - which would give each of them a different execution
// ID for what the user asked for as a single request.
func HeadersFromMetadata(md capabilities.RequestMetadata) http.Header {
	header := http.Header{}
	value := reflect.ValueOf(md)
	t := value.Type()

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := HeaderPrefix + headerSuffix(f.Name)
		field := value.Field(i)

		if field.Kind() == reflect.Slice && field.Type() != reflect.TypeFor[time.Time]() {
			// One header per entry, which is how they are read back.
			for j := range field.Len() {
				if entry := formatEntry(field.Index(j)); entry != "" {
					header.Add(name, entry)
				}
			}
			continue
		}
		if rendered := formatDefault(field); rendered != "" {
			header.Set(name, rendered)
		}
	}
	return header
}

// formatEntry renders one element of a repeated field, in the key=value form
// assignEntry reads.
func formatEntry(entry reflect.Value) string {
	if entry.Kind() == reflect.String {
		return entry.String()
	}
	if entry.Kind() != reflect.Struct || entry.NumField() != 2 {
		return ""
	}
	first, second := entry.Field(0), entry.Field(1)
	if first.Kind() != reflect.String || second.Kind() != reflect.String {
		return ""
	}
	return first.String() + "=" + second.String()
}

// formatDefault renders a default the way the UI would show it, and the way
// MetadataFromHeaders parses it back.
func formatDefault(v reflect.Value) string {
	switch {
	case v.Type() == reflect.TypeFor[time.Time]():
		t := v.Interface().(time.Time)
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	case v.Kind() == reflect.Slice:
		return ""
	case v.CanUint():
		return strconv.FormatUint(v.Uint(), 10)
	case v.CanInt():
		return strconv.FormatInt(v.Int(), 10)
	default:
		return fmt.Sprint(v.Interface())
	}
}

// MetadataFromHeaders builds the metadata a capability is called with from the
// headers a request carried.
//
// get is passed the header name and returns every value under it, so this works
// against an http.Header and against gRPC metadata.MD alike - both are
// map[string][]string, and a repeated field is repeated headers rather than one
// header holding a list.
//
// Anything absent or blank is filled in from defaultMetadata, so a caller that
// specifies nothing still sends a usable request. An unparseable number or
// timestamp is an error rather than a silent default: the caller asked for
// something specific and got neither it nor a warning otherwise.
func MetadataFromHeaders(get func(name string) []string) (capabilities.RequestMetadata, error) {
	metadata := defaultMetadata()
	out := reflect.ValueOf(&metadata).Elem()
	t := out.Type()

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		values := nonBlank(get(HeaderPrefix + headerSuffix(f.Name)))
		if len(values) == 0 {
			continue
		}
		if err := assign(out.Field(i), values); err != nil {
			return capabilities.RequestMetadata{}, userErrorf("%s: %s", HeaderPrefix+headerSuffix(f.Name), err)
		}
	}

	// The execution timestamp is the one default that cannot be a constant: a
	// fixed one would put every request at the same instant.
	if metadata.ExecutionTimestamp.IsZero() {
		metadata.ExecutionTimestamp = time.Now().UTC()
	}

	if err := validateHexFields(metadata); err != nil {
		return capabilities.RequestMetadata{}, err
	}
	return metadata, nil
}

// validateHexFields rejects a value the report encoder would refuse.
//
// Checked here so it is reported as the field it is. Left to the capability, the
// same mistake is not an error at all: the consensus plugin cannot encode the
// metadata, so it produces no report, nothing is transmitted back, and the
// request the user made sits until it expires. What they are told is "timeout
// exceeded", which names neither the field nor the length - and takes twenty
// seconds to say it.
func validateHexFields(md capabilities.RequestMetadata) error {
	for _, f := range []struct {
		name  string
		value string
		bytes int
	}{
		{"WorkflowExecutionID", md.WorkflowExecutionID, executionIDBytes},
		{"WorkflowID", md.WorkflowID, workflowIDBytes},
		{"WorkflowName", md.WorkflowName, workflowNameBytes},
		{"WorkflowOwner", md.WorkflowOwner, workflowOwnerBytes},
	} {
		decoded, err := hex.DecodeString(strings.TrimPrefix(f.value, "0x"))
		if err != nil {
			return userErrorf("%s%s: %s must be hex, because a consensus report carries it on-chain: %s",
				HeaderPrefix, headerSuffix(f.name), f.name, err)
		}
		// WorkflowName is padded to length by the encoder, so a short one is fine.
		if len(decoded) > f.bytes || (len(decoded) < f.bytes && f.name != "WorkflowName") {
			return userErrorf("%s%s: %s must be %d hex bytes (%d characters), got %d",
				HeaderPrefix, headerSuffix(f.name), f.name, f.bytes, f.bytes*2, len(decoded))
		}
	}
	return nil
}

func nonBlank(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// assign writes header values into one field, by its Go type.
func assign(field reflect.Value, values []string) error {
	switch {
	case field.Type() == reflect.TypeFor[time.Time]():
		parsed, err := time.Parse(time.RFC3339, values[0])
		if err != nil {
			return fmt.Errorf("expected an RFC3339 timestamp: %w", err)
		}
		field.Set(reflect.ValueOf(parsed))
		return nil

	case field.Kind() == reflect.Slice:
		slice := reflect.MakeSlice(field.Type(), 0, len(values))
		for _, v := range values {
			entry := reflect.New(field.Type().Elem()).Elem()
			if err := assignEntry(entry, v); err != nil {
				return err
			}
			slice = reflect.Append(slice, entry)
		}
		field.Set(slice)
		return nil

	case field.CanUint():
		n, err := strconv.ParseUint(values[0], 10, 64)
		if err != nil {
			return fmt.Errorf("expected a number: %w", err)
		}
		field.SetUint(n)
		return nil

	case field.CanInt():
		n, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			return fmt.Errorf("expected a number: %w", err)
		}
		field.SetInt(n)
		return nil

	case field.Kind() == reflect.String:
		field.SetString(values[0])
		return nil

	default:
		return fmt.Errorf("unsupported field type %s", field.Type())
	}
}

// assignEntry fills one element of a repeated field from a single header value.
//
// A two-string struct (SpendLimit) is "type=limit", the same key=value form the
// standalone config uses for its own pair-valued settings.
func assignEntry(entry reflect.Value, value string) error {
	if entry.Kind() == reflect.String {
		entry.SetString(value)
		return nil
	}
	if entry.Kind() != reflect.Struct || entry.NumField() != 2 {
		return fmt.Errorf("unsupported repeated element type %s", entry.Type())
	}

	key, limit, found := strings.Cut(value, "=")
	if !found || key == "" {
		return fmt.Errorf("invalid entry %q: expected key=value", value)
	}
	for i, v := range []string{key, limit} {
		f := entry.Field(i)
		if f.Kind() != reflect.String {
			return fmt.Errorf("unsupported repeated element type %s", entry.Type())
		}
		f.SetString(v)
	}
	return nil
}
