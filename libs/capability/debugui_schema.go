package capability

import (
	"reflect"
	"strings"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
)

type formField struct {
	Name     string
	Type     string
	Kind     string
	Message  string
	Enum     []string
	Key      string
	Value    *formField
	List     bool
	Oneof    string
	Optional bool
}

type formSchema map[string][]formField

func (s formSchema) message(md protoreflect.MessageDescriptor) string {
	name := string(md.FullName())
	if _, seen := s[name]; seen {
		return name
	}
	s[name] = nil // so a message that holds itself stops here

	var fields []formField
	all := md.Fields()
	for i := range all.Len() {
		fd := all.Get(i)
		var f formField
		if fd.IsMap() {
			value := s.field(fd.MapValue())
			f = formField{Kind: "map", Type: "map<" + fd.MapKey().Kind().String() + ", " + value.Type + ">", Key: fd.MapKey().Kind().String(), Value: &value}
		} else {
			f = s.field(fd)
		}
		f.Name = fd.TextName()
		f.List = fd.IsList()
		if o := fd.ContainingOneof(); o != nil && !o.IsSynthetic() {
			f.Oneof = string(o.Name())
		}
		f.Optional = fd.HasPresence() && f.Oneof == ""
		fields = append(fields, f)
	}
	s[name] = fields
	return name
}

func (s formSchema) field(fd protoreflect.FieldDescriptor) formField {
	t := fd.Kind().String()
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return formField{Type: t, Kind: "bool"}
	case protoreflect.StringKind:
		return formField{Type: t, Kind: "string"}
	case protoreflect.BytesKind:
		return formField{Type: t, Kind: "bytes"}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return formField{Type: t, Kind: "float"}
	case protoreflect.EnumKind:
		f := formField{Type: string(fd.Enum().FullName()), Kind: "enum"}
		values := fd.Enum().Values()
		for i := range values.Len() {
			f.Enum = append(f.Enum, string(values.Get(i).Name()))
		}
		return f
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return s.messageField(fd.Message())
	default:
		// protojson reads every integer from a string, which keeps 64-bit ones exact.
		return formField{Type: t, Kind: "int"}
	}
}

func (s formSchema) messageField(md protoreflect.MessageDescriptor) formField {
	name := md.FullName()
	f := formField{Type: string(name)}
	switch name {
	case bigIntName:
		f.Kind = "bigint"
	case decimalName:
		f.Kind = "decimal"
	case "google.protobuf.Timestamp":
		f.Kind = "timestamp"
	case "google.protobuf.Duration":
		f.Kind = "duration"
	case "google.protobuf.FieldMask":
		f.Kind = "string"
	case "google.protobuf.Struct", "google.protobuf.Value", "google.protobuf.ListValue", "google.protobuf.Any":
		f.Kind = "json"
	default:
		// A wrapper, such as google.protobuf.Int64Value, is written as the value it wraps.
		if value := md.Fields().ByName("value"); value != nil && md.Fields().Len() == 1 &&
			strings.HasPrefix(string(name), "google.protobuf.") {
			f.Kind = s.field(value).Kind
			return f
		}
		f.Kind, f.Message = "message", s.message(md)
	}
	return f
}

func (s formSchema) goStruct(t reflect.Type) string {
	name := t.String()
	if _, seen := s[name]; seen {
		return name
	}
	s[name] = nil

	var fields []formField
	for i := range t.NumField() {
		f := s.goField(t.Field(i).Type)
		f.Name = t.Field(i).Name
		fields = append(fields, f)
	}
	s[name] = fields
	return name
}

func (s formSchema) goField(t reflect.Type) formField {
	f := formField{Type: t.String()}
	switch {
	case t == reflect.TypeFor[time.Time]():
		f.Kind = "timestamp"
	case t.Kind() == reflect.Slice:
		f = s.goField(t.Elem())
		f.List = true
	case t.Kind() == reflect.Struct:
		f.Kind, f.Message = "message", s.goStruct(t)
	case t.Kind() == reflect.String:
		f.Kind = "string"
	case t.Kind() == reflect.Bool:
		f.Kind = "bool"
	default:
		f.Kind = "number"
	}
	return f
}
