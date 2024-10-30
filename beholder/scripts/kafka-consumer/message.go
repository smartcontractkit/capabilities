package main

type Value struct {
	StringValue string `json:"stringValue"`
	BytesValue  string `json:"bytesValue,omitempty"`
}

type Attribute struct {
	Key   string `json:"key"`
	Value Value  `json:"value"`
}

type Resource struct {
	Attributes []Attribute `json:"attributes"`
}

type LogRecord struct {
	ObservedTimeUnixNano string      `json:"observedTimeUnixNano"`
	Body                 Value       `json:"body"`
	Attributes           []Attribute `json:"attributes"`
	TraceID              string      `json:"traceId"`
	SpanID               string      `json:"spanId"`
}

type Scope struct {
	Name string `json:"name"`
}

type ScopeLog struct {
	Scope      Scope       `json:"scope"`
	LogRecords []LogRecord `json:"logRecords"`
}

type ResourceLog struct {
	Resource  Resource   `json:"resource"`
	ScopeLogs []ScopeLog `json:"scopeLogs"`
	SchemaURL string     `json:"schemaUrl"`
}

type Message struct {
	ResourceLogs []ResourceLog `json:"resourceLogs"`
}
