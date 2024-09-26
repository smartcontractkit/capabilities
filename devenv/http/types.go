package main

import (
	"fmt"

	common "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

type body struct {
	ID      string `json:"id"`
	Request *struct {
		Metadata *struct {
			WorkflowID               string `json:"workflow_id"`
			WorkflowOwner            string `json:"workflow_owner"`
			WorkflowExecutionID      string `json:"workflow_execution_id"`
			WorkflowName             string `json:"workflow_name"`
			WorkflowDonID            uint32 `json:"workflow_don_id"`
			WorkflowDonConfigVersion uint32 `json:"workflow_don_config_version"`
			ReferenceID              string `json:"reference_id"`
		} `json:"metadata,omitempty"`
		Inputs *any `json:"inputs,omitempty"`
		Config *any `json:"config,omitempty"`
	} `json:"request,omitempty"`
}

func (b body) toCapabilityRequest() (common.CapabilityRequest, error) {
	if b.Request == nil {
		return common.CapabilityRequest{}, fmt.Errorf("no request found")
	}

	var (
		cfg *values.Map
		in  *values.Map
		md  common.RequestMetadata
		err error
	)

	if b.Request.Inputs != nil {
		in, err = values.WrapMap(*b.Request.Inputs)
		if err != nil {
			return common.CapabilityRequest{}, err
		}
	}

	if b.Request.Config != nil {
		cfg, err = values.WrapMap(*b.Request.Config)
		if err != nil {
			return common.CapabilityRequest{}, err
		}
	}

	if b.Request.Metadata != nil {
		md = common.RequestMetadata{
			WorkflowID:               b.Request.Metadata.WorkflowID,
			WorkflowOwner:            b.Request.Metadata.WorkflowOwner,
			WorkflowExecutionID:      b.Request.Metadata.WorkflowExecutionID,
			WorkflowName:             b.Request.Metadata.WorkflowName,
			WorkflowDonID:            b.Request.Metadata.WorkflowDonID,
			WorkflowDonConfigVersion: b.Request.Metadata.WorkflowDonConfigVersion,
			ReferenceID:              b.Request.Metadata.ReferenceID,
		}
	}

	return common.CapabilityRequest{
		Inputs:   in,
		Config:   cfg,
		Metadata: md,
	}, nil
}

type capabilityInfo struct {
	ID             string `json:"id"`
	CapabilityType string `json:"capability_type"`
	Description    string `json:"description"`
}
