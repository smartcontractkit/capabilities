// Package nodeinfo is report info as the node's keyring reads it: a protobuf Struct whose keyBundleName names the key
// bundle that signs the report.
package nodeinfo

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	keyBundleName = "keyBundleName"
	value         = "value"
)

// Encode is the info of a report signed with keyBundle, carrying value for whoever transmits it.
func Encode(keyBundle string, v []byte) ([]byte, error) {
	fields := map[string]any{keyBundleName: keyBundle}
	if v != nil {
		fields[value] = string(v)
	}

	info, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, err
	}

	return proto.MarshalOptions{Deterministic: true}.Marshal(info)
}

// Decode is the key bundle and value Encode encoded into info.
func Decode(info []byte) (string, []byte, error) {
	s := &structpb.Struct{}
	if err := proto.Unmarshal(info, s); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal report info: %w", err)
	}

	keyBundle, ok := s.Fields[keyBundleName]
	if !ok {
		return "", nil, errors.New("report info has no key bundle")
	}

	var v []byte
	if f, ok := s.Fields[value]; ok {
		v = []byte(f.GetStringValue())
	}

	return keyBundle.GetStringValue(), v, nil
}
