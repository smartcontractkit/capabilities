package common

const (
	ErrorOutgoingRatelimitGlobal        = "global limit of outgoing gateways requests has been exceeded"
	ErrorOutgoingRatelimitWorkflowOwner = "workflow owner exceeded limit of gateways requests"
	ErrorIncomingRatelimitGlobal        = "message from gateway exceeded global rate limit"
	ErrorIncomingRatelimitSender        = "message from gateway exceeded per sender rate limits"
)
