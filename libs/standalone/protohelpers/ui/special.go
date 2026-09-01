package ui

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

// Some messages are worse to read and worse to fill in than the number they
// stand for. A BigInt is a sign and a big-endian byte string, so typing -1000
// into a form means working out that it is sign -1 and the base64 of 0x03e8;
// reading one back means doing it in reverse. A Decimal is that plus an exponent.
//
// So the page offers a box holding the number, and does the arithmetic. The two
// halves of that are here and in form.js: this says which messages are special
// and where in a response they turn up, and the browser does the rendering.
//
// Which messages, and where, both come from the descriptors. The type names are
// read off the generated types, so renaming one upstream is a compile error here
// rather than a widget that silently stops appearing; the response paths are
// walked from the method's own output type, so nothing has to recognise a BigInt
// from the shape of the JSON it arrives as.

// The messages the page renders as a number.
var (
	bigIntTypeName  = (&valuespb.BigInt{}).ProtoReflect().Descriptor().FullName()
	decimalTypeName = (&valuespb.Decimal{}).ProtoReflect().Descriptor().FullName()
)

// SpecialPath is one place in a response where a special message turns up.
//
// A "[]" suffix on a segment marks a repeated field and "{}" a map, telling the
// browser to iterate rather than index straight in.
type SpecialPath struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

// SpecialConfig is what the browser is told: which messages are special, and
// where each method's response holds them.
type SpecialConfig struct {
	// BigInt and Decimal are the type names as grpcui labels a field with them,
	// which is how the form finds one to replace. Sent rather than hard-coded in
	// the script for the same reason they are read off the generated types here.
	BigInt  string `json:"bigInt"`
	Decimal string `json:"decimal"`
	// Methods maps a method, as the page names it, to the paths in its response
	// that hold a special message, and Requests does the same for its request.
	//
	// Both, because both are shown: a response is read, and a request is read back
	// out of history. They are separate maps because a method's request and
	// response are different types and a path only means something against one of
	// them.
	Methods  map[string][]SpecialPath `json:"methods"`
	Requests map[string][]SpecialPath `json:"requests"`
	// BytesMethods and BytesRequests are the same two maps for bytes fields, which
	// arrive as base64 and are read as hex everywhere else. The paths are here for
	// the same reason the special ones are: a base64 string and a plain string are
	// the same JSON, so which is which has to come from the descriptor rather than
	// from the shape of what arrived.
	BytesMethods  map[string][]string `json:"bytesMethods"`
	BytesRequests map[string][]string `json:"bytesRequests"`
}

// specialConfig describes every method this server offers.
func (s *Server) specialConfig() SpecialConfig {
	cfg := SpecialConfig{
		BigInt:        string(bigIntTypeName),
		Decimal:       string(decimalTypeName),
		Methods:       map[string][]SpecialPath{},
		Requests:      map[string][]SpecialPath{},
		BytesMethods:  map[string][]string{},
		BytesRequests: map[string][]string{},
	}

	for _, m := range s.methods {
		// The shape the page names a method by: grpcui addresses a method as
		// "/<service full name>/<method>".
		key := "/" + m.GetService().GetFullyQualifiedName() + "/" + m.GetName()

		if output := m.GetOutputType().UnwrapMessage(); output != nil {
			if paths := specialPaths(output); len(paths) > 0 {
				cfg.Methods[key] = paths
			}
			if paths := bytesPaths(output); len(paths) > 0 {
				cfg.BytesMethods[key] = paths
			}
		}
		if input := m.GetInputType().UnwrapMessage(); input != nil {
			if paths := specialPaths(input); len(paths) > 0 {
				cfg.Requests[key] = paths
			}
			if paths := bytesPaths(input); len(paths) > 0 {
				cfg.BytesRequests[key] = paths
			}
		}
	}
	return cfg
}

// specialPaths walks a message descriptor and returns a path for every special
// message reachable from it.
//
// Field names come from the descriptor because that is what a response is keyed
// by: grpcui marshals with the original proto names.
func specialPaths(md protoreflect.MessageDescriptor) []SpecialPath {
	var paths []SpecialPath

	// onPath guards against a message that transitively contains itself, which
	// values.v1.Value does - a Map holds Values, and a Value can be a Map.
	onPath := map[protoreflect.FullName]bool{md.FullName(): true}

	var walk func(m protoreflect.MessageDescriptor, prefix string)
	walk = func(m protoreflect.MessageDescriptor, prefix string) {
		fields := m.Fields()
		for i := range fields.Len() {
			fd := fields.Get(i)

			var (
				target  protoreflect.MessageDescriptor
				segment string
			)
			switch {
			case fd.IsMap():
				if fd.MapValue().Kind() != protoreflect.MessageKind {
					continue
				}
				target = fd.MapValue().Message()
				segment = prefix + string(fd.Name()) + "{}"
			case fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind:
				target = fd.Message()
				segment = prefix + string(fd.Name())
				if fd.IsList() {
					segment += "[]"
				}
			default:
				continue
			}

			// Decimal is checked first, and neither descends: a Decimal holds a
			// BigInt coefficient, and reporting that separately would offer a box
			// for a number that is already part of the one beside it.
			switch target.FullName() {
			case decimalTypeName:
				paths = append(paths, SpecialPath{Path: segment, Type: string(decimalTypeName)})
				continue
			case bigIntTypeName:
				paths = append(paths, SpecialPath{Path: segment, Type: string(bigIntTypeName)})
				continue
			}

			if onPath[target.FullName()] {
				continue
			}
			onPath[target.FullName()] = true
			walk(target, segment+".")
			delete(onPath, target.FullName())
		}
	}

	walk(md, "")
	return paths
}

// bytesPaths walks a message descriptor and returns a path for every bytes field
// reachable from it, in the same notation specialPaths uses: "[]" for a repeated
// field and "{}" for a map.
//
// Which is what tells the page a string is bytes at all. base64 and text are both
// JSON strings, so a page that guessed would sooner or later show a name as hex.
func bytesPaths(md protoreflect.MessageDescriptor) []string {
	var paths []string

	// The same guard specialPaths needs, for the same message: a values.v1.Map
	// holds Values and a Value can be a Map.
	onPath := map[protoreflect.FullName]bool{md.FullName(): true}

	var walk func(m protoreflect.MessageDescriptor, prefix string)
	walk = func(m protoreflect.MessageDescriptor, prefix string) {
		fields := m.Fields()
		for i := range fields.Len() {
			fd := fields.Get(i)

			if fd.IsMap() {
				if fd.MapValue().Kind() == protoreflect.BytesKind {
					paths = append(paths, prefix+string(fd.Name())+"{}")
					continue
				}
				if fd.MapValue().Kind() != protoreflect.MessageKind {
					continue
				}
				target := fd.MapValue().Message()
				if skipForBytes(target) {
					continue
				}
				onPath[target.FullName()] = true
				walk(target, prefix+string(fd.Name())+"{}.")
				delete(onPath, target.FullName())
				continue
			}

			switch fd.Kind() {
			case protoreflect.BytesKind:
				segment := prefix + string(fd.Name())
				if fd.IsList() {
					segment += "[]"
				}
				paths = append(paths, segment)
			case protoreflect.MessageKind, protoreflect.GroupKind:
				target := fd.Message()
				// A BigInt is a sign and a byte string, and the page already shows
				// it as the number it stands for - offering its magnitude as hex
				// would be offering half of a value that is no longer on screen.
				if skipForBytes(target) || onPath[target.FullName()] {
					continue
				}
				segment := prefix + string(fd.Name())
				if fd.IsList() {
					segment += "[]"
				}
				onPath[target.FullName()] = true
				walk(target, segment+".")
				delete(onPath, target.FullName())
			}
		}
	}

	walk(md, "")
	return paths
}

// skipForBytes says whether a nested message is one the page already renders as
// something other than its fields.
func skipForBytes(md protoreflect.MessageDescriptor) bool {
	return md.FullName() == bigIntTypeName || md.FullName() == decimalTypeName
}
