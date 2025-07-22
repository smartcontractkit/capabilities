# Consensus Capability

## Overview

The consensus capability is a standard capability that supports two requests:

Simple

The Simple request type is used to get consensus on a values.Value across nodes in a DON. A Simple request contains the 
node's observation and a consensus descriptor.  The consensus descriptor describes the consensus algorithm to be used. 
Each node will receive the consensus values.Value.

Report

The Report request type is used to create a report that can be sent to the forwarder contract (https://github.com/smartcontractkit/chainlink-evm/blob/develop/contracts/src/v0.8/keystone/KeystoneForwarder.sol). 
Each node in the DON submits a report and if consensus is reached the metadata required by the forwarder contract is pre-pended
to the report which will then be signed by each node in the DON. For consensus to be reached f+1 nodes must agree on the report.
Each node will receive the report with pre-pended metadata and the signatures from all nodes.

## Implementation

The core of the capability sits in two places: plugin.go and the CalculateOutcomeForObservations
method.  The CalculateOutcomeForObservations method is called by the plugin to calculate the consensus outcome based on the
observations received from the nodes and a consensus descriptor.  

The plugin.go file contains an implementation of the libocr (https://github.com/smartcontractkit/libocr/blob/master/offchainreporting2plus/ocr3types/plugin.go) 
plugin interface.  The plugin batches multiple consensus requests into a single consensus round.  The query created by the leader node contains the request IDs for a batch along 
 their corresponding consensus descriptors and metadata.  The query is validated by each node before returning an observation
by checking that the consensus descriptor and metadata in the query for a given request matches the consensus descriptor and metadata
of the node's local request, this ensures that the leader node cannot unduly influence the consensus outcome.

In the case of the Report request, the consensus values.Value is unwrapped to []byte before prepending the metadata 
to ensure that the report its associated signatures are in the correct format for the forwarder contract.