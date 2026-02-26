package utils

import (
	"errors"
	"strings"
)

const (
	MaxKeyLen   = 128
	MaxValueLen = 1028
)

var (
	ErrKeyContainsColon   = errors.New("key must not contain ':' character")
	ErrValueContainsColon = errors.New("value must not contain ':' character")
	ErrKeyEmpty           = errors.New("key must be between 1 and 128 characters")
	ErrKeyTooLong         = errors.New("key must be between 1 and 128 characters")
	ErrValueEmpty         = errors.New("value must be between 1 and 1028 characters")
	ErrValueTooLong       = errors.New("value must be between 1 and 1028 characters")
)

func ValidateKey(key string) error {
	if len(key) == 0 {
		return ErrKeyEmpty
	}
	if len(key) > MaxKeyLen {
		return ErrKeyTooLong
	}
	if strings.Contains(key, ":") {
		return ErrKeyContainsColon
	}
	return nil
}

func ValidateValue(value string) error {
	if len(value) == 0 {
		return ErrValueEmpty
	}
	if len(value) > MaxValueLen {
		return ErrValueTooLong
	}
	if strings.Contains(value, ":") {
		return ErrValueContainsColon
	}
	return nil
}

