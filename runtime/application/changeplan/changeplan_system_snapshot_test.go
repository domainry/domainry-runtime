package changeplan

import (
	"context"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestSystemSnapshotHelpersOwnNormalizationAndProvenance(t *testing.T) {
	sources := changeplanprojection.AddSystemResourceSource(nil, "object", "customer", "Customer", "builder")
	sources = changeplanprojection.AddSystemResourceSource(sources, "object", "customer", "Duplicate", "builder")
	if len(sources) != 1 || sources[0].SourceKind != "builder" {
		t.Fatalf("sources=%#v", sources)
	}

	runtime := changeplanprojection.BusinessRuntimeStateSnapshot{}
	runtime.Normalize()
	if runtime.RunningWorkflowProcesses == nil || runtime.Scheduler.Definitions == nil || runtime.Connections == nil {
		t.Fatalf("runtime snapshot was not normalized: %#v", runtime)
	}
	if hash := changeplanprojection.SystemSnapshotHash(struct {
		Key string `json:"key"`
	}{Key: "value"}); len(hash) != 64 {
		t.Fatalf("hash=%q", hash)
	}
}

func TestReferenceServiceAllowsSchemaOnlyGraphWithoutOptionalRuntime(t *testing.T) {
	service := NewChangePlanReferenceApplicationService(func(context.Context, principalmodel.Principal) ReferenceSchema {
		return ReferenceSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}
	}, nil, nil)
	graph, err := service.Graph(t.Context(), accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Permissions: []string{ActionBusinessReferenceGraph}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) == 0 || graph.Hash == "" {
		t.Fatalf("graph=%#v", graph)
	}
}

func TestRuntimeStateProjectionsFilterAndSortOwnerFacts(t *testing.T) {
	processes := changeplanprojection.ProjectWorkflowProcesses([]workflowmodel.WorkflowProcessInstance{{ID: "done", Status: "completed"}, {ID: "running", WorkflowKey: "approval", Status: "running", CurrentNodeIDs: []string{"review"}}})
	if len(processes) != 1 || processes[0].ID != "running" || processes[0].CurrentNodeIDs[0] != "review" {
		t.Fatalf("workflow projection=%#v", processes)
	}
	connections := changeplanprojection.ProjectIntegrationConnections([]integrationsdk.Connection{{Key: "z", Status: "disabled"}, {Key: "a", Status: "active"}}, func(value integrationsdk.Connection) bool { return value.Status == "active" })
	if len(connections) != 2 || connections[0].Key != "a" || !connections[0].Ready || connections[1].Ready {
		t.Fatalf("connection projection=%#v", connections)
	}
	outbox := changeplanprojection.ProjectPublicationHandoff([]publicationmodel.Message{{ID: "old", UpdatedAt: "2026-01-01T00:00:00Z"}, {ID: "new", UpdatedAt: "2026-02-01T00:00:00Z"}})
	if len(outbox) != 2 || outbox[0].ID != "new" {
		t.Fatalf("outbox projection=%#v", outbox)
	}
}
