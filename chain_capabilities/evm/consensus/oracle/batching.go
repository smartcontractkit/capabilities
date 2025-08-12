package oracle

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const (
	mapKeyTagSize = 1 // field 1, tag size 1.
	mapValTagSize = 1 // field 2, tag size 2.
)

func sizeOfNewMapElement(mapKeyID int32, key string, value proto.Message) int {
	keySize := mapKeyTagSize + protowire.SizeBytes(len(key))
	valSize := mapValTagSize + protowire.SizeBytes(proto.Size(value))
	mapTagSize := protowire.SizeVarint(protowire.EncodeTag(protowire.Number(mapKeyID), protowire.BytesType)) // maps are tried as messages and presented on wire as Bytes
	return mapTagSize + protowire.SizeBytes(keySize+valSize)
}

func hasCapacityToAdd(currentSize int, mapKeyID int32, key string, value proto.Message, maxSize int) (int, bool) {
	newElementSize := sizeOfNewMapElement(mapKeyID, key, value)
	return currentSize + newElementSize, currentSize+newElementSize <= maxSize
}
