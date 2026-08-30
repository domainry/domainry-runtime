package testkit

import (
	"testing"

	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
)

type runtimeServicesMetadataOverride struct {
	appschemarepository.ApplicationSchemaRepository
}
type runtimeServicesEvidenceOverride struct {
	changeplanrepository.ChangePlanEvidenceRepository
}
type runtimeServicesWorkflowDefinitionsOverride struct {
	workflowcontract.WorkflowDefinitionStore
}

func TestNewRuntimeServicesAcceptsExplicitMetadataAndEvidenceOverrides(t *testing.T) {
	services := NewRuntimeServices(t.Context(), RuntimeServicesConfig{
		ApplicationSchemaRepository: &runtimeServicesMetadataOverride{},
		BusinessEvidence:            &runtimeServicesEvidenceOverride{},
		WorkflowDefinitions:         &runtimeServicesWorkflowDefinitionsOverride{},
	})
	if services == nil {
		t.Fatal("runtime services were nil")
	}
}
