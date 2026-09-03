package businesssystem

import (
	"context"
	"errors"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type businessSystemEvidenceStub struct {
	values []businessseedmodel.BusinessSeedProvenance
	err    error
}

type businessSystemDefinitionsStub struct {
	list     func(context.Context, metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error)
	get      func(context.Context, string, string) (metadatasdk.Definition, bool, error)
	snapshot func(context.Context) (metadatasdk.DefinitionSnapshot, error)
}

func (stub businessSystemDefinitionsStub) List(ctx context.Context, query metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	if stub.list == nil {
		return nil, nil
	}
	return stub.list(ctx, query)
}

func (stub businessSystemDefinitionsStub) Get(ctx context.Context, resourceType, resourceKey string) (metadatasdk.Definition, bool, error) {
	if stub.get != nil {
		return stub.get(ctx, resourceType, resourceKey)
	}
	return metadatasdk.Definition{}, false, nil
}

func (stub businessSystemDefinitionsStub) Snapshot(ctx context.Context) (metadatasdk.DefinitionSnapshot, error) {
	if stub.snapshot != nil {
		return stub.snapshot(ctx)
	}
	return metadatasdk.DefinitionSnapshot{}, nil
}

func (stub businessSystemEvidenceStub) ListSeedProvenance(context.Context) ([]businessseedmodel.BusinessSeedProvenance, error) {
	return stub.values, stub.err
}

func businessSystemTestDependencies() BusinessSystemApplicationDependencies {
	schema := appschemamodel.ApplicationSchemaSnapshot{SchemaHash: "schema-hash", Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}, Workflows: []definitionmodel.WorkflowSchema{{Key: "approval"}}}
	return BusinessSystemApplicationDependencies{
		FeaturePermissions: func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
			return recordcontract.RecordFeaturePermissionSnapshot{}, nil
		},
		SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
			return schema
		},
		Definitions: businessSystemDefinitionsStub{},
		Runtime: BusinessSystemRuntimeProjectionDependencies{
			WorkflowProcesses: func(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
				return nil, nil
			},
			AutomationRules: func(context.Context, principalmodel.Principal) ([]automationmodel.AutomationRuleSchema, error) {
				return nil, nil
			},
			AutomationExecutions: func(context.Context, automationmodel.AutomationExecutionFilter, principalmodel.Principal) (automationprojection.AutomationExecutionHistory, error) {
				return automationprojection.AutomationExecutionHistory{}, nil
			},
			ConnectorCatalog: func(context.Context, principalmodel.Principal) ([]connectormodel.ConnectorSchema, error) {
				return nil, nil
			},
			IntegrationConnections: func(context.Context, principalmodel.Principal) ([]integrationsdk.Connection, error) {
				return nil, nil
			},
			PublicationHandoff: func(context.Context, string, string, int, principalmodel.Principal) ([]publicationmodel.Message, error) {
				return nil, nil
			},
			SchedulerDefinitions: func(context.Context, principalmodel.Principal) ([]recordmodel.Record, error) {
				return []recordmodel.Record{{ID: "nightly"}}, nil
			},
			SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
				return schema
			},
			ListRecords: func(_ context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
				return recordmodel.RecordPageResult{Total: 7, Items: []recordmodel.Record{{ID: objectKey + "-1"}}}, nil
			},
		},
	}
}

func TestBusinessSystemSnapshotSeparatesAdministratorAndLimitedVisibility(t *testing.T) {
	service := NewBusinessSystemApplicationService(businessSystemTestDependencies())
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{ActionBusinessSystemSnapshot}})
	snapshot, err := service.Snapshot(t.Context(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaHash == "" || snapshot.SchemaHash != snapshot.Schema.SchemaHash || snapshot.Schema.SnapshotVersion != snapshot.SchemaHash || snapshot.ObjectRecordCounts["customer"] != 7 || len(snapshot.Schema.Workflows) != 1 || snapshot.Schema.Workflows[0].Key != "approval" {
		t.Fatalf("administrator snapshot=%#v", snapshot)
	}
	if snapshot.ResourceVisibility["runtime_state.integrations"] != "visible" || snapshot.AuthoringContractHash == "" {
		t.Fatalf("administrator governance projection=%#v", snapshot)
	}

	limited := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{})
	snapshot, err = service.Snapshot(t.Context(), limited)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ResourceVisibility["runtime_state.integrations"] != "hidden" {
		t.Fatalf("limited snapshot visibility=%#v", snapshot.ResourceVisibility)
	}
}

func TestBusinessSystemSnapshotIndexAvoidsFullProjectionReads(t *testing.T) {
	dependencies := businessSystemTestDependencies()
	schemaCalls, metadataCalls, evidenceCalls, expensiveCalls := 0, 0, 0, 0
	schema := appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "string"}}}}}
	dependencies.SchemaForPrincipal = func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
		schemaCalls++
		return schema
	}
	dependencies.Definitions = businessSystemDefinitionsStub{snapshot: func(context.Context) (metadatasdk.DefinitionSnapshot, error) {
		metadataCalls++
		return metadatasdk.DefinitionSnapshot{Definitions: []metadatasdk.Definition{
			{ResourceType: "field", ResourceKey: "customer.name", ObjectKey: "customer", SchemaHash: "field-hash", Payload: []byte(`{"key":"name","type":"string"}`)},
			{ResourceType: "workflow", ResourceKey: "ignored-by-business-resource-index", SchemaHash: "workflow-hash"},
		}}, nil
	}}
	dependencies.Evidence = businessSystemEvidenceStub{values: []businessseedmodel.BusinessSeedProvenance{{}}}
	dependencies.FeaturePermissions = func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
		expensiveCalls++
		return recordcontract.RecordFeaturePermissionSnapshot{}, nil
	}
	dependencies.Runtime.WorkflowProcesses = func(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
		expensiveCalls++
		return nil, nil
	}
	dependencies.Runtime.ListRecords = func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		expensiveCalls++
		return recordmodel.RecordPageResult{}, nil
	}
	service := NewBusinessSystemApplicationService(dependencies)
	service.evidence = businessSystemEvidenceCounter{calls: &evidenceCalls}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{ActionBusinessSystemSnapshot}})

	index, err := service.SnapshotIndex(t.Context(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if schemaCalls != 1 || metadataCalls != 1 || evidenceCalls != 1 || expensiveCalls != 0 {
		t.Fatalf("index reads schema=%d metadata=%d evidence=%d expensive=%d", schemaCalls, metadataCalls, evidenceCalls, expensiveCalls)
	}
	if index.ResourceCount != 1 || index.ResourceCounts["field"] != 1 || len(index.ResourceCollections) != 1 || index.ResourceCollections[0].CollectionHash == "" {
		t.Fatalf("index summary=%#v", index)
	}
	if index.ResourceVisibility["runtime_state.automation"] != "available_on_demand" || index.Links.RuntimeStateIndex == "" {
		t.Fatalf("index lost on-demand runtime discoverability: %#v", index)
	}
}

type businessSystemEvidenceCounter struct{ calls *int }

func (counter businessSystemEvidenceCounter) ListSeedProvenance(context.Context) ([]businessseedmodel.BusinessSeedProvenance, error) {
	(*counter.calls)++
	return []businessseedmodel.BusinessSeedProvenance{{}}, nil
}

func TestBusinessSystemSnapshotResourceQueriesAreBoundedAndExact(t *testing.T) {
	definitions := []metadatasdk.Definition{
		{ResourceType: "field", ResourceKey: "customer.email", ObjectKey: "customer", SchemaHash: "email-hash", Payload: []byte(`{"key":"email","type":"email"}`)},
		{ResourceType: "field", ResourceKey: "customer.name", ObjectKey: "customer", SchemaHash: "name-hash", Payload: []byte(`{"key":"name","type":"string"}`)},
	}
	dependencies := businessSystemTestDependencies()
	dependencies.Definitions = businessSystemDefinitionsStub{
		snapshot: func(context.Context) (metadatasdk.DefinitionSnapshot, error) {
			return metadatasdk.DefinitionSnapshot{Definitions: definitions}, nil
		},
		get: func(_ context.Context, resourceType, resourceKey string) (metadatasdk.Definition, bool, error) {
			for _, definition := range definitions {
				if definition.ResourceType == resourceType && definition.ResourceKey == resourceKey {
					return definition, true, nil
				}
			}
			return metadatasdk.Definition{}, false, nil
		},
	}
	service := NewBusinessSystemApplicationService(dependencies)
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{ActionBusinessSystemSnapshot}})

	page, valid, err := service.SnapshotResourcePage(t.Context(), admin, "field", "", 1)
	if err != nil || !valid || len(page.Items) != 1 || page.Items[0].ResourceKey != "customer.email" || page.NextCursor == "" {
		t.Fatalf("page=%#v valid=%t err=%v", page, valid, err)
	}
	detail, found, err := service.SnapshotResourceDetail(t.Context(), admin, "field", "customer.email")
	if err != nil || !found || detail.ResourceHash != "email-hash" || string(detail.Definition) != `{"key":"email","type":"email"}` {
		t.Fatalf("detail=%#v found=%t err=%v", detail, found, err)
	}
	limited := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{})
	if _, _, err := service.SnapshotResourcePage(t.Context(), limited, "field", "", 1); err == nil {
		t.Fatal("limited principal received resource page")
	}
}

func TestBusinessSystemSnapshotRejectsUnknownPrincipalAndPropagatesOwnerError(t *testing.T) {
	dependencies := businessSystemTestDependencies()
	service := NewBusinessSystemApplicationService(dependencies)
	if _, err := service.Snapshot(t.Context(), principalmodel.Principal{}); err == nil {
		t.Fatal("unknown principal unexpectedly received a snapshot")
	}
	want := errors.New("permission projection unavailable")
	dependencies.FeaturePermissions = func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
		return recordcontract.RecordFeaturePermissionSnapshot{}, want
	}
	service = NewBusinessSystemApplicationService(dependencies)
	if _, err := service.Snapshot(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); !errors.Is(err, want) {
		t.Fatalf("Snapshot() error=%v want=%v", err, want)
	}
}

func TestBusinessSystemRuntimeProjectionConfiguration(t *testing.T) {
	dependencies := businessSystemTestDependencies()
	service := NewBusinessSystemApplicationService(dependencies)
	if !service.RuntimeProjectionConfigured() || (*BusinessSystemApplicationService)(nil).RuntimeProjectionConfigured() {
		t.Fatal("runtime projection configuration check is incorrect")
	}
}

func TestBusinessSystemRuntimeStatePropagatesEveryOwnerFailure(t *testing.T) {
	want := errors.New("owner unavailable")
	tests := []struct {
		name   string
		mutate func(*BusinessSystemApplicationDependencies)
	}{
		{name: "workflow", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.WorkflowProcesses = func(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
				return nil, want
			}
		}},
		{name: "automation rules", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.AutomationRules = func(context.Context, principalmodel.Principal) ([]automationmodel.AutomationRuleSchema, error) {
				return nil, want
			}
		}},
		{name: "automation runs", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.AutomationExecutions = func(context.Context, automationmodel.AutomationExecutionFilter, principalmodel.Principal) (automationprojection.AutomationExecutionHistory, error) {
				return automationprojection.AutomationExecutionHistory{}, want
			}
		}},
		{name: "connectors", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.ConnectorCatalog = func(context.Context, principalmodel.Principal) ([]connectormodel.ConnectorSchema, error) {
				return nil, want
			}
		}},
		{name: "connections", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.IntegrationConnections = func(context.Context, principalmodel.Principal) ([]integrationsdk.Connection, error) {
				return nil, want
			}
		}},
		{name: "outbox", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.PublicationHandoff = func(context.Context, string, string, int, principalmodel.Principal) ([]publicationmodel.Message, error) {
				return nil, want
			}
		}},
		{name: "scheduler definitions", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.SchedulerDefinitions = func(context.Context, principalmodel.Principal) ([]recordmodel.Record, error) {
				return nil, want
			}
		}},
		{name: "idempotency", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.IdempotencyStatus = func(context.Context, string) (deploymentmodel.IdempotencyOperationalStatus, error) {
				return deploymentmodel.IdempotencyOperationalStatus{}, want
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := businessSystemTestDependencies()
			test.mutate(&dependencies)
			service := NewBusinessSystemApplicationService(dependencies)
			_, err := service.RuntimeStateSnapshot(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
			if !errors.Is(err, want) {
				t.Fatalf("RuntimeStateSnapshot() error=%v want=%v", err, want)
			}
		})
	}
}

func TestBusinessSystemResourceProjectionPreservesEvidence(t *testing.T) {
	dependencies := businessSystemTestDependencies()
	dependencies.Definitions = businessSystemDefinitionsStub{snapshot: func(context.Context) (metadatasdk.DefinitionSnapshot, error) {
		return metadatasdk.DefinitionSnapshot{Definitions: []metadatasdk.Definition{{ResourceType: "object", ResourceKey: "customer", Name: "Customer", DisabledAt: "now"}}}, nil
	}}
	service := NewBusinessSystemApplicationService(dependencies)
	sources, err := service.businessResourceSources(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if err != nil || len(sources) != 1 || sources[0].SourceKind != "unknown" || !sources[0].Disabled {
		t.Fatalf("sources=%#v err=%v", sources, err)
	}
	if got := businessSystemValueOrDefault(" value ", "fallback"); got != " value " {
		t.Fatalf("valueOrDefault()=%q", got)
	}
	if got := businessSystemValueOrDefault(" ", "fallback"); got != "fallback" {
		t.Fatalf("valueOrDefault()=%q", got)
	}

	service.SetEvidenceRepository(nil)
	(*BusinessSystemApplicationService)(nil).SetEvidenceRepository(nil)
}

func TestBusinessSystemResourceProjectionPropagatesMetadataFailure(t *testing.T) {
	want := errors.New("metadata unavailable")
	dependencies := businessSystemTestDependencies()
	dependencies.Definitions = businessSystemDefinitionsStub{snapshot: func(context.Context) (metadatasdk.DefinitionSnapshot, error) {
		return metadatasdk.DefinitionSnapshot{}, want
	}}
	service := NewBusinessSystemApplicationService(dependencies)
	if _, err := service.businessResourceSources(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); !errors.Is(err, want) {
		t.Fatalf("businessResourceSources() error=%v want=%v", err, want)
	}
}

func TestBusinessSystemAdministratorSnapshotPropagatesEveryProjectionFailure(t *testing.T) {
	want := errors.New("projection unavailable")
	tests := []struct {
		name   string
		mutate func(*BusinessSystemApplicationDependencies)
	}{
		{name: "resource sources", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Definitions = businessSystemDefinitionsStub{snapshot: func(context.Context) (metadatasdk.DefinitionSnapshot, error) {
				return metadatasdk.DefinitionSnapshot{}, want
			}}
		}},
		{name: "runtime state", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.WorkflowProcesses = func(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
				return nil, want
			}
		}},
		{name: "record counts", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.ListRecords = func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
				return recordmodel.RecordPageResult{}, want
			}
		}},
		{name: "seed evidence", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Evidence = businessSystemEvidenceStub{err: want}
		}},
	}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{ActionBusinessSystemSnapshot}})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := businessSystemTestDependencies()
			test.mutate(&dependencies)
			_, err := NewBusinessSystemApplicationService(dependencies).Snapshot(t.Context(), admin)
			if err == nil {
				t.Fatal("Snapshot() unexpectedly succeeded")
			}
			if test.name != "seed evidence" && !errors.Is(err, want) {
				t.Fatalf("Snapshot() error=%v want=%v", err, want)
			}
		})
	}
}

func TestBusinessSystemRemainingProjectionOutcomes(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{ActionBusinessSystemSnapshot}})
	dependencies := businessSystemTestDependencies()
	dependencies.Evidence = businessSystemEvidenceStub{values: []businessseedmodel.BusinessSeedProvenance{{}}}
	dependencies.Runtime.IdempotencyStatus = func(context.Context, string) (deploymentmodel.IdempotencyOperationalStatus, error) {
		return deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{"ready": 1}}, nil
	}
	service := NewBusinessSystemApplicationService(dependencies)
	if snapshot, err := service.Snapshot(t.Context(), principal); err != nil || len(snapshot.SeedRecords) != 1 || snapshot.RuntimeState.Idempotency.Backlog["ready"] != 1 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}

}
