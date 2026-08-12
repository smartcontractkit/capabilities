package don2don

// DispatcherConfig is what the dispatcher needs to know about itself. It is a struct rather than
// the interface core declares in core/config, because a config here is a value a binary fills in
// (from flags, in the standalone framework) rather than a view onto a node's TOML.
type DispatcherConfig struct {
	// SupportedVersion is stamped on every outgoing message.
	SupportedVersion int
	// ReceiverBufferSize is how many messages may queue for one receiver before they are dropped.
	ReceiverBufferSize int
	// RateLimit caps inbound traffic, in total and per sender.
	RateLimit DispatcherRateLimit
	// SendToSharedPeer routes outgoing messages through the DON-to-DON shared peer rather than the
	// legacy external peer.
	SendToSharedPeer bool
}

// DispatcherRateLimit caps how much inbound traffic is accepted.
type DispatcherRateLimit struct {
	GlobalRPS      float64
	GlobalBurst    int
	PerSenderRPS   float64
	PerSenderBurst int
}
