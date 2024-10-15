package target

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/kvstore/kvcap"
)

var _ capabilities.TargetCapability = (*capability)(nil)

type capability struct {
	logger logger.Logger
	store  core.KeyValueStore
}

type Params struct {
	Logger logger.Logger
	Store  core.KeyValueStore
}

type ReportV1Metadata struct {
	Version             uint8
	WorkflowExecutionID [32]byte
	Timestamp           uint32
	DonID               uint32
	DonConfigVersion    uint32
	WorkflowCID         [32]byte
	WorkflowName        [10]byte
	WorkflowOwner       [20]byte
	ReportID            [2]byte
}

func (rm ReportV1Metadata) Encode() ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, rm)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (rm ReportV1Metadata) Length() int {
	bytes, err := rm.Encode()
	if err != nil {
		return 0
	}
	return len(bytes)
}

func New(p Params) *capability {
	return &capability{
		logger: p.Logger,
		store:  p.Store,
	}
}

func (c *capability) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo("kv-store-target@1.0.0", capabilities.CapabilityTypeTarget, "Writes KV-pairs from a SignedReport to a key-value store")
}

type Request struct {
	Metadata capabilities.RequestMetadata
	Inputs   kvcap.Inputs
}

type StoreMappingRequest struct {
	// Keys corresponds to the JSON schema field "keys".
	Keys []string `json:"keys" yaml:"keys" mapstructure:"keys"`

	// Values corresponds to the JSON schema field "values".
	Values [][]uint8 `json:"values" yaml:"values" mapstructure:"values"`
}

func evaluate(rawRequest capabilities.CapabilityRequest) (r Request, err error) {
	r.Metadata = rawRequest.Metadata

	if rawRequest.Inputs == nil {
		return r, fmt.Errorf("missing inputs field")
	}

	const signedReportField = "signedReport"
	signedReport, ok := rawRequest.Inputs.Underlying[signedReportField]
	if !ok {
		return r, fmt.Errorf("missing required field %s", signedReportField)
	}

	if err = signedReport.UnwrapTo(&r.Inputs.SignedReport); err != nil {
		return r, fmt.Errorf("failed to unwrap signed report: %v", err)
	}

	return r, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.logger.Debug("Executing", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	request, err := evaluate(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode signed report: %v", err)
	}

	// Ignore metadata.
	var metadata ReportV1Metadata
	reportData := request.Inputs.SignedReport.Report[metadata.Length():]

	var storeRequest StoreMappingRequest
	err = json.Unmarshal(reportData, &storeRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	c.logger.Debug("Decoded signed report", "WorkflowID", request.Metadata.WorkflowID, "WorkflowExecutionID", request.Metadata.WorkflowExecutionID, "ReportVersion", request.Inputs.SignedReport)

	for i := range storeRequest.Keys {
		var index = i
		key := storeRequest.Keys[index]
		c.logger.Infow("Storing new key", "key", key)
		if err = c.store.Store(ctx, key, storeRequest.Values[index]); err != nil {
			return capabilities.CapabilityResponse{}, err
		}

		val, err := c.store.Get(ctx, key)
		if err != nil {
			return capabilities.CapabilityResponse{}, err
		}
		if !bytes.Equal(val, []byte(storeRequest.Values[index])) {
			return capabilities.CapabilityResponse{}, fmt.Errorf("stored value does not match expected value: expected: %v got: %v", storeRequest.Values[index], val)
		}

		c.logger.Infow("Key stored successfully", "key", key)
	}
	c.logger.Debug("Values stored", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	return capabilities.CapabilityResponse{}, nil
}

func (c *capability) RegisterToWorkflow(ctx context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.logger.Debug("Registering to workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")

	return nil
}

func (c *capability) UnregisterFromWorkflow(ctx context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.logger.Debug("Unregistering from workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")
	return nil
}
