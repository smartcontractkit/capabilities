# Mock Capability
This capability allows you to dynamically create and manage capabilities at runtime. Its primary use case is for load testing and performance measurement.

## How It Works
It adheres to the standard capability API and functions like any other capability. Upon initialization, it sets up a gRPC service, which enables the creation and control of capabilities through gRPC calls.

The [GRPC service](internal/pb/proxy.proto) has the following interface:
```protobuf
service Proxy {
  // Retrieve information about all capabilities available on the node
  rpc List(ListRequest) returns (ListResponse){}

  // Create a mock capability and register it with the node
  rpc CreateCapability(CapabilityInfo) returns(google.protobuf.Empty) {}

  // Send data through a mock trigger
  rpc SendTriggerEvent(SendTriggerEventRequest) returns(google.protobuf.Empty) {}

  // Subscribe to a trigger (includes all triggers, not limited to mock triggers), 
  // creates a stream that send trigger events
  rpc RegisterTrigger(TriggerRegistrationRequest) returns(stream TriggerResponse) {}

  // Unsubscribe from a trigger (includes all triggers, not limited to mock triggers)
  rpc UnregisterTrigger(TriggerRegistrationRequest) returns(google.protobuf.Empty) {}//NOOP

  // Establish a bidirectional streaming service. When Execute is called, it streams requests and allows streaming responses back
  rpc HookExecutables(stream ExecutableResponse) returns(stream ExecutableRequest) {}

  // Subscribe to a workflow (includes all executable capabilities, not limited to mocks)
  rpc RegisterToWorkflow(RegisterToWorkflowRequest) returns (google.protobuf.Empty){}

  // Unsubscribe from a workflow (includes all executable capabilities, not limited to mocks)
  rpc UnregisterFromWorkflow(UnregisterFromWorkflowRequest) returns (google.protobuf.Empty){}

  // Invoke the Execute method on an executable capability (includes all executable capabilities, not limited to mocks)
  rpc Execute(ExecutableRequest) returns (CapabilityResponse){}
}
```

### Configuration
As a standard capability, the node must be configured with a job specification to start it.

The mock capability requires minimal configuration. It expects a port number for launching the gRPC service and optionally accepts **DefaultMocks** (predefined capabilities that will be created automatically when the job starts).
```toml
type = "standardcapabilities"
schemaVersion = 1
externalJobID = "..."
name = "mock-capabilitie"
command = "path-to-mock-cap-binary"
config = """
port=3456
[[DefaultMocks]]
id="some-trigger@1.0.2"
description="description of some-trigger capability"
type="trigger"
[[DefaultMocks]]
id="some-target@1.2.3"
description="description of some target capability"
type="target"
"""
```