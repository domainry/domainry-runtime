package testkit

import (
	"testing"

	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
)

type runtimeServicesMetadataOverride struct {
	metadatarepository.MetadataRepository
}
type runtimeServicesEvidenceOverride struct {
	changeplanrepository.ChangePlanEvidenceRepository
}
type runtimeServicesWorkflowDefinitionsOverride struct {
	workflowcontract.WorkflowDefinitionStore
}

func TestNewRuntimeServicesAcceptsExplicitMetadataAndEvidenceOverrides(t *testing.T) {
	services := NewRuntimeServices(t.Context(), RuntimeServicesConfig{
		MetadataRepository:  &runtimeServicesMetadataOverride{},
		BusinessEvidence:    &runtimeServicesEvidenceOverride{},
		WorkflowDefinitions: &runtimeServicesWorkflowDefinitionsOverride{},
	})
	if services == nil {
		t.Fatal("runtime services were nil")
	}
}
