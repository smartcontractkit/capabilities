package ui

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// TriggerIDHeader is what a subscription's trigger ID travels in.
//
// Its own header rather than a RequestMetadata field, because it is not one: the
// metadata says what workflow a call is being made on behalf of, and the trigger
// ID says which registration this is. It is filled in the same way, though - left
// blank, the page mints one - so the Advanced section treats it like the rest.
const TriggerIDHeader = "X-CRE-TRIGGER-ID"

// triggerIDPrefix marks a trigger ID this page minted. Unlike the metadata's hex
// fields nothing constrains this value, so it is readable: it is the row a user
// picks out of a sidebar.
const triggerIDPrefix = "ui-trigger-"

// triggerIDEntropy is how many random bytes a minted trigger ID carries.
const triggerIDEntropy = 6

// TriggerIDFromHeaders is the trigger ID a request named, or a fresh one.
//
// A subscription is identified by its trigger ID, so a caller that does not name
// one gets a new subscription, and a caller that names one that is already running
// joins it. That is what makes "subscribe two instances now and a third later" one
// subscription: the third names the same trigger ID.
func TriggerIDFromHeaders(get func(name string) []string) string {
	for _, value := range get(TriggerIDHeader) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return NewTriggerID()
}

// NewTriggerID mints one. Random rather than a counter, because the instances of
// an embed run are separate processes' worth of state in one binary and a counter
// in each of them would collide.
func NewTriggerID() string {
	buf := make([]byte, triggerIDEntropy)
	if _, err := rand.Read(buf); err != nil {
		// Not worth failing a debug subscription over. The clock still separates
		// this from the one before it.
		return triggerIDPrefix + hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000")))
	}
	return triggerIDPrefix + hex.EncodeToString(buf)
}
