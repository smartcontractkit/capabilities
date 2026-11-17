package capability

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

var bigIntName = (&valuespb.BigInt{}).ProtoReflect().Descriptor().FullName()
var decimalName = (&valuespb.Decimal{}).ProtoReflect().Descriptor().FullName()

var anyName = (&anypb.Any{}).ProtoReflect().Descriptor().FullName()

// maxFixedDigits is the most decimal places a Decimal is written out with; past it, it's written with an exponent, so
// a tiny exponent can't make a huge string.
const maxFixedDigits = 100

func toPageJSON(msg proto.Message, hexBytes bool) (json.RawMessage, error) {
	tree, err := jsonTree(protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}, msg)
	if err != nil {
		return nil, err
	}

	show := func(fd protoreflect.FieldDescriptor, v any) (any, bool, error) { return showPageJSON(fd, v, hexBytes) }
	if tree, err = walkPageRoot(msg.ProtoReflect().Descriptor(), tree, show); err != nil {
		return nil, err
	}

	return json.Marshal(tree)
}

func fromPageJSON(b []byte, msg proto.Message) error {
	var tree any
	if err := unmarshalJSONNumbers(b, &tree); err != nil {
		return err
	}

	tree, err := walkPageRoot(msg.ProtoReflect().Descriptor(), tree, readPageJSON)
	if err != nil {
		return err
	}

	return fromJSONTree(tree, msg)
}

func showPageJSON(fd protoreflect.FieldDescriptor, v any, hexBytes bool) (any, bool, error) {
	switch {
	case v == nil:
		return v, true, nil
	case fd.Kind() == protoreflect.BytesKind:
		s, _ := v.(string)
		// Base64 that starts like hex would be read back as hex, so it's shown as hex, which is the same bytes.
		if !hexBytes && !strings.HasPrefix(s, "0x") {
			return v, true, nil
		}

		b, err := base64.StdEncoding.DecodeString(s)
		return "0x" + hex.EncodeToString(b), true, err
	case !isNumberMessage(fd):
		return v, false, nil
	}

	// A number is shown only if it reads back as exactly what was sent, since a BigInt or Decimal can encode
	// one number several ways. Anything else is shown as its fields.
	s, err := numberString(fd, v)
	if err != nil {
		return v, false, nil
	}

	back, _, err := readPageJSON(fd, s)
	if err != nil {
		return v, false, nil
	}

	sent, got := newNumberMessage(fd), newNumberMessage(fd)
	if fromJSONTree(v, sent) != nil || fromJSONTree(back, got) != nil || !proto.Equal(sent, got) {
		return v, false, nil
	}

	return s, true, nil
}

func isNumberMessage(fd protoreflect.FieldDescriptor) bool {
	return fd.Message() != nil && (fd.Message().FullName() == bigIntName || fd.Message().FullName() == decimalName)
}

func newNumberMessage(fd protoreflect.FieldDescriptor) proto.Message {
	if fd.Message().FullName() == bigIntName {
		return &valuespb.BigInt{}
	}

	return &valuespb.Decimal{}
}

func numberString(fd protoreflect.FieldDescriptor, v any) (string, error) {
	if fd.Message().FullName() == bigIntName {
		n := &valuespb.BigInt{}
		err := fromJSONTree(v, n)
		return valuespb.NewIntFromBigInt(n).String(), err
	}

	d := &valuespb.Decimal{}
	if err := fromJSONTree(v, d); err != nil {
		return "", err
	}

	if d.Coefficient == nil {
		return "", fmt.Errorf("%s has no coefficient", fd.FullName())
	}

	coefficient := valuespb.NewIntFromBigInt(d.Coefficient)
	if d.Exponent > 0 || d.Exponent < -maxFixedDigits {
		return fmt.Sprintf("%se%d", coefficient, d.Exponent), nil
	}

	return decimal.NewFromBigInt(coefficient, d.Exponent).StringFixed(-d.Exponent), nil
}

func readPageJSON(fd protoreflect.FieldDescriptor, v any) (any, bool, error) {
	s, isString := v.(string)
	switch {
	case !isString:
		return v, false, nil
	case fd.Kind() == protoreflect.BytesKind:
		trimmed, isHex := strings.CutPrefix(s, "0x")
		if !isHex {
			return v, true, nil
		}

		b, err := hex.DecodeString(trimmed)
		return base64.StdEncoding.EncodeToString(b), true, err
	case fd.Message() == nil:
		return v, false, nil
	case fd.Message().FullName() == bigIntName:
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, true, fmt.Errorf("%s: %q is not an integer", fd.FullName(), s)
		}

		t, err := jsonTree(protojson.MarshalOptions{}, valuespb.NewBigIntFromInt(n))
		return t, true, err
	case fd.Message().FullName() == decimalName:
		d, err := decimal.NewFromString(s)
		if err != nil {
			return nil, true, fmt.Errorf("%s: %w", fd.FullName(), err)
		}

		t, err := jsonTree(protojson.MarshalOptions{}, &valuespb.Decimal{
			Coefficient: valuespb.NewBigIntFromInt(d.Coefficient()), Exponent: d.Exponent(),
		})

		return t, true, err
	}

	return v, false, nil
}

// pageJSONLeaf returns whether it rewrote v, so the walk does not descend into it.
type pageJSONLeaf func(fd protoreflect.FieldDescriptor, v any) (any, bool, error)

func walkPageJSON(md protoreflect.MessageDescriptor, v any, leaf pageJSONLeaf) (any, error) {
	object, ok := v.(map[string]any)
	if !ok {
		return v, nil
	}

	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		// protojson reads either name.
		for _, key := range slices.Compact([]string{fd.TextName(), fd.JSONName()}) {
			var err error
			switch v := object[key].(type) {
			case nil:
			case map[string]any:
				if !fd.IsMap() {
					object[key], err = walkPageJSONValue(fd, v, leaf)
					break
				}

				for k, entry := range v {
					if v[k], err = walkPageJSONValue(fd.MapValue(), entry, leaf); err != nil {
						break
					}
				}
			case []any:
				for i, entry := range v {
					if v[i], err = walkPageJSONValue(fd, entry, leaf); err != nil {
						break
					}
				}

			default:
				object[key], err = walkPageJSONValue(fd, v, leaf)
			}

			if err != nil {
				return nil, err
			}
		}
	}

	return object, nil
}

// walkPageRoot is walkPageJSON for a whole message, whose JSON is its value's if it's an Any or a wrapper.
func walkPageRoot(md protoreflect.MessageDescriptor, v any, leaf pageJSONLeaf) (any, error) {
	switch {
	case md.FullName() == anyName:
		return v, nil
	case isWrapper(md):
		return walkPageJSONValue(md.Fields().ByName("value"), v, leaf)
	}

	return walkPageJSON(md, v, leaf)
}

func isWrapper(md protoreflect.MessageDescriptor) bool {
	return md.ParentFile().Path() == wrapperspb.File_google_protobuf_wrappers_proto.Path()
}

func walkPageJSONValue(fd protoreflect.FieldDescriptor, v any, leaf pageJSONLeaf) (any, error) {
	md := fd.Message()
	switch {
	case md != nil && md.FullName() == anyName:
		// An Any's JSON is its message's, which the walk can't know, so it's left as it is.
		return v, nil
	case md != nil && isWrapper(md):
		// A wrapper's JSON is its value's.
		fd = md.Fields().ByName("value")
		md = nil
	case fd.Kind() != protoreflect.BytesKind && md == nil:
		return v, nil
	}

	if out, done, err := leaf(fd, v); done || err != nil || md == nil {
		return out, err
	}

	return walkPageJSON(md, v, leaf)
}

func jsonTree(opts protojson.MarshalOptions, msg proto.Message) (any, error) {
	b, err := opts.Marshal(msg)
	if err != nil {
		return nil, err
	}

	var t any
	return t, unmarshalJSONNumbers(b, &t)
}

func fromJSONTree(tree any, msg proto.Message) error {
	b, err := json.Marshal(tree)
	if err != nil {
		return err
	}

	return protojson.Unmarshal(b, msg)
}

func unmarshalJSONNumbers(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}
