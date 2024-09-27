package target

type kvRequest struct {
	WorkflowExecutionID string
	RequestType         string
	KVPairs             map[string][]byte
}

func NewWriteRequest(weID string, kvPairs map[string][]byte) *kvRequest {
	return &kvRequest{
		WorkflowExecutionID: weID,
		RequestType:         "write",
		KVPairs:             kvPairs,
	}
}
