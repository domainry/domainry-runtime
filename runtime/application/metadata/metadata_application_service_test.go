// Metadata domain service tests.
package metadata

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"context"
	"encoding/json"
	"errors"

	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
)

type auditEventFactoryStub struct{}

func (auditEventFactoryStub) NewAuditEvent(_ context.Context, request auditcontract.AuditAppendRequest) auditmodel.AuditEvent {
	return auditmodel.AuditEvent{
		Event: request.Event, ObjectKey: request.ObjectKey, RecordID: request.RecordID,
		Summary: request.Summary, Before: request.Before, After: request.After, Metadata: request.Metadata,
	}
}

type upsertMetadataRepository struct {
	metadatarepository.MetadataRepository
	before           metadatamodel.MetadataDefinition
	saved            metadatamodel.MetadataDefinition
	request          metadatamodel.MetadataDefinitionUpsertRequest
	disabled         bool
	versions         []metadatamodel.MetadataDefinitionVersion
	rollback         metadatamodel.MetadataDefinition
	audit            auditmodel.AuditEvent
	refreshErrorText string
	refreshCalls     int
}

func (r *upsertMetadataRepository) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (metadatamodel.MetadataDefinition, bool, error) {
	return r.before, r.before.ResourceKey != "", nil
}

func (r *upsertMetadataRepository) UpsertDefinition(_ context.Context, _ principalmodel.SystemScope, _, _ string, request metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinition, error) {
	r.request = request
	return r.saved, nil
}

func (r *upsertMetadataRepository) PublishDefinition(_ context.Context, _ principalmodel.SystemScope, _, _ string, request metadatamodel.MetadataDefinitionUpsertRequest, audit auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	r.request, r.audit = request, audit
	return r.saved, nil
}

func (r *upsertMetadataRepository) CompleteDefinitionRefresh(_ context.Context, _ principalmodel.SystemScope, _, _, _, errorText string) error {
	r.refreshCalls++
	r.refreshErrorText = errorText
	return nil
}

func (r *upsertMetadataRepository) DisableDefinition(context.Context, principalmodel.SystemScope, string, string) error {
	r.disabled = true
	return nil
}

func (r *upsertMetadataRepository) ListDefinitionVersions(context.Context, principalmodel.SystemScope, string, string) ([]metadatamodel.MetadataDefinitionVersion, error) {
	return append([]metadatamodel.MetadataDefinitionVersion(nil), r.versions...), nil
}

func (r *upsertMetadataRepository) RollbackDefinition(_ context.Context, _ principalmodel.SystemScope, _, _ string, _ metadatamodel.MetadataDefinitionRollbackRequest, audit auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	r.audit = audit
	return r.rollback, nil
}

func (r *upsertMetadataRepository) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	return manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}, nil
}

func (r *upsertMetadataRepository) SyncManifest(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error {
	return nil
}

type upsertMetadataRuntime struct {
	snapshot metadatamodel.MetadataSchemaSnapshot
}

func (*upsertMetadataRuntime) ApplyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ViewSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []metadatamodel.DictionarySchema, integrationmodel.IntegrationSchema, []reportmodel.ReportSchema, []definitionmodel.EntryPointSchema, []agentmodel.SkillSchema, []agentmodel.AgentSchema, []profilebindingmodel.Binding) {
}
func (r *upsertMetadataRuntime) Schema() metadatamodel.MetadataSchemaSnapshot { return r.snapshot }

type upsertWorkflowInitializer struct{ err error }

func (w upsertWorkflowInitializer) InitializePublishedWorkflowDefinitions(context.Context, []definitionmodel.WorkflowSchema, principalmodel.SystemScope) error {
	return w.err
}

func TestServiceOwnsDefinitionUpsertReloadAndAudit(t *testing.T) {
	repository := &upsertMetadataRepository{
		before: metadatamodel.MetadataDefinition{ResourceType: "action", ResourceKey: "order.approve", Payload: json.RawMessage(`{"old":true}`)},
		saved:  metadatamodel.MetadataDefinition{ResourceType: "action", ResourceKey: "order.approve", SchemaVersion: "2", SchemaHash: "hash-2", Payload: json.RawMessage(`{"new":true}`)},
	}
	runtime := &upsertMetadataRuntime{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}}
	service := NewMetadataApplicationService(MetadataApplicationDependencies{Repository: repository, Runtime: runtime, Workflows: upsertWorkflowInitializer{}, Audit: auditEventFactoryStub{}})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	definition, snapshot, err := service.upsertMetadataDefinitionWithOptions(t.Context(), " action ", " order.approve ", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"draft":true}`)}, admin, MetadataUpsertDefinitionOptions{
		Normalize: func(_ context.Context, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
			if resourceType != "action" || resourceKey != "order.approve" {
				t.Fatalf("identity not normalized: %q %q", resourceType, resourceKey)
			}
			request.Payload = json.RawMessage(`{"normalized":true}`)
			return request, nil
		},
	})
	if err != nil {
		t.Fatalf("upsert definition: %v", err)
	}
	if definition.SchemaHash != "hash-2" || len(snapshot.Objects) != 1 || string(repository.request.Payload) != `{"normalized":true}` || repository.audit.Event != "metadata_definition.saved" || repository.audit.Before["payload"] == nil {
		t.Fatalf("unexpected result: definition=%+v snapshot=%+v request=%s audit=%+v", definition, snapshot, repository.request.Payload, repository.audit)
	}
}

func TestDefinitionPublishMarksDurableIntentForReconciliationWhenRuntimeRefreshFails(t *testing.T) {
	repository := &upsertMetadataRepository{saved: metadatamodel.MetadataDefinition{ResourceType: "action", ResourceKey: "order.approve", SchemaVersion: "1", SchemaHash: "hash-1", Payload: json.RawMessage(`{"new":true}`)}}
	want := errors.New("workflow refresh unavailable")
	service := NewMetadataApplicationService(MetadataApplicationDependencies{Repository: repository, Runtime: &upsertMetadataRuntime{}, Workflows: upsertWorkflowInitializer{err: want}, Audit: auditEventFactoryStub{}})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	_, _, err := service.upsertMetadataDefinitionWithOptions(t.Context(), "action", "order.approve", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"draft":true}`)}, admin, MetadataUpsertDefinitionOptions{Normalize: func(_ context.Context, _, _ string, request metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
		return request, nil
	}})
	if !errors.Is(err, want) || repository.refreshCalls != 1 || repository.refreshErrorText != want.Error() {
		t.Fatalf("err=%v refresh_calls=%d refresh_error=%q", err, repository.refreshCalls, repository.refreshErrorText)
	}
}

func TestServiceDoesNotDuplicateAuditForDefinitionPublishReplay(t *testing.T) {
	current := metadatamodel.MetadataDefinition{ResourceType: "automation_rule", ResourceKey: "order.sync", SchemaVersion: "3", SchemaHash: "hash-3", Payload: json.RawMessage(`{"key":"order.sync"}`)}
	repository := &upsertMetadataRepository{before: current, saved: current}
	runtime := &upsertMetadataRuntime{snapshot: metadatamodel.MetadataSchemaSnapshot{AutomationRules: []automationmodel.AutomationRuleSchema{{Key: "order.sync"}}}}
	service := NewMetadataApplicationService(MetadataApplicationDependencies{Repository: repository, Runtime: runtime, Workflows: upsertWorkflowInitializer{}, Audit: auditEventFactoryStub{}})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	_, _, err := service.upsertMetadataDefinitionWithOptions(t.Context(), "automation_rule", "order.sync", metadatamodel.MetadataDefinitionUpsertRequest{Payload: current.Payload}, admin, MetadataUpsertDefinitionOptions{
		Normalize: func(_ context.Context, _, _ string, request metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
			return request, nil
		},
	})
	if err != nil {
		t.Fatalf("publish replay err=%v", err)
	}
}

func TestServiceRejectsDefinitionUpsertWithoutAdmin(t *testing.T) {
	service := NewMetadataApplicationService(MetadataApplicationDependencies{})
	_, _, err := service.upsertMetadataDefinitionWithOptions(t.Context(), "action", "order.approve", metadatamodel.MetadataDefinitionUpsertRequest{}, principalmodel.Principal{}, MetadataUpsertDefinitionOptions{})
	if err == nil {
		t.Fatal("expected authorization error")
	}
}

func TestServiceOwnsDefinitionDisableImpactReloadAndAudit(t *testing.T) {
	repository := &upsertMetadataRepository{before: metadatamodel.MetadataDefinition{ResourceType: "action", ResourceKey: "order.approve", Payload: json.RawMessage(`{"key":"order.approve"}`)}}
	service := NewMetadataApplicationService(MetadataApplicationDependencies{Repository: repository, Runtime: &upsertMetadataRuntime{}, Workflows: upsertWorkflowInitializer{}, Audit: auditEventFactoryStub{}})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	audited := false
	err := service.disableMetadataDefinitionWithOptions(t.Context(), " action ", " order.approve ", admin, MetadataDisableDefinitionOptions{
		ResolveImpact: func(_ context.Context, resourceType, resourceKey string, _ principalmodel.Principal) (DisableReferenceImpact, error) {
			if resourceType != "action" || resourceKey != "order.approve" {
				t.Fatalf("identity not normalized: %q %q", resourceType, resourceKey)
			}
			return DisableReferenceImpact{ResourceType: "action", GraphHash: "graph-1", DirectConsumers: 1}, nil
		},
		Audit: func(_ context.Context, _, _ string, _ principalmodel.Principal, before, after, metadata map[string]any) {
			audited = before["resource_key"] == "order.approve" && after["disabled"] == true && metadata["reference_graph_hash"] == "graph-1"
		},
	})
	if err != nil || !repository.disabled || !audited {
		t.Fatalf("disable err=%v disabled=%v audited=%v", err, repository.disabled, audited)
	}
}

func TestServiceBlocksDefinitionDisableBeforeRepositoryMutation(t *testing.T) {
	repository := &upsertMetadataRepository{}
	service := NewMetadataApplicationService(MetadataApplicationDependencies{Repository: repository})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	err := service.disableMetadataDefinitionWithOptions(t.Context(), "action", "order.approve", admin, MetadataDisableDefinitionOptions{
		ResolveImpact: func(context.Context, string, string, principalmodel.Principal) (DisableReferenceImpact, error) {
			return DisableReferenceImpact{ResourceType: "action", GraphHash: "graph-1", DirectConsumers: 2, DeletionBlocked: true}, nil
		},
	})
	if err == nil || err.Error() != "backend.reference.delete_blocked" || repository.disabled {
		t.Fatalf("err=%v disabled=%v", err, repository.disabled)
	}
}

func TestServiceOwnsDefinitionRollbackContractRepositoryReloadAndAudit(t *testing.T) {
	repository := &upsertMetadataRepository{
		before:   metadatamodel.MetadataDefinition{ResourceType: "action", ResourceKey: "order.approve", SchemaVersion: "3", SchemaHash: "hash-3", Payload: json.RawMessage(`{"version":3}`)},
		versions: []metadatamodel.MetadataDefinitionVersion{{SchemaVersion: "2", SchemaHash: "hash-2", Payload: json.RawMessage(`{"version":2}`)}},
		rollback: metadatamodel.MetadataDefinition{ResourceType: "action", ResourceKey: "order.approve", SchemaVersion: "2", SchemaHash: "hash-2"},
	}
	service := NewMetadataApplicationService(MetadataApplicationDependencies{Repository: repository, Runtime: &upsertMetadataRuntime{}, Workflows: upsertWorkflowInitializer{}, Audit: auditEventFactoryStub{}})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	request := metadatamodel.MetadataDefinitionRollbackRequest{
		TargetVersion: "2", ExpectedSchemaHash: "hash-3", ExpectedReferenceGraphHash: "graph-1",
		BusinessReason: "restore stable action", ChangePlanID: "plan-1",
		AuthoringContractVersion: "authoring-v1", AuthoringContractHash: "contract-hash",
	}
	definition, _, err := service.rollbackMetadataDefinitionWithOptions(t.Context(), " action ", " order.approve ", request, admin, MetadataRollbackDefinitionOptions{
		AuthoringContractVersion: "authoring-v1", AuthoringContractHash: "contract-hash",
		ResolveImpact: func(_ context.Context, resourceType, resourceKey string, _ principalmodel.Principal) (RollbackReferenceImpact, error) {
			if resourceType != "action" || resourceKey != "order.approve" {
				t.Fatalf("identity not normalized: %q %q", resourceType, resourceKey)
			}
			return RollbackReferenceImpact{GraphHash: "graph-1", DirectConsumers: 2}, nil
		},
	})
	if err != nil || definition.SchemaVersion != "2" || repository.audit.Event != "metadata_definition.rolled_back" || repository.audit.Metadata["direct_consumers"] != 2 {
		t.Fatalf("definition=%+v audit=%+v err=%v", definition, repository.audit, err)
	}
}
