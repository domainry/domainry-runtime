package businesssystem

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type businessSystemEvidenceStub struct {
	values []businessseedmodel.BusinessSeedProvenance
	err    error
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
		ApplicationDefinitions: func(context.Context, string, string, principalmodel.Principal) ([]appschemamodel.ApplicationDefinition, error) {
			return nil, nil
		},
		FrontendSnapshot: func(context.Context, principalmodel.Principal) (deploymentmodel.FrontendCapabilitySnapshot, error) {
			return deploymentmodel.FrontendCapabilitySnapshot{Status: "ready"}, nil
		},
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
			ConnectorCatalog: func(context.Context, principalmodel.Principal) ([]integrationmodel.ConnectorSchema, error) {
				return nil, nil
			},
			IntegrationConnections: func(context.Context, principalmodel.Principal) ([]integrationmodel.IntegrationConnection, error) {
				return nil, nil
			},
			IntegrationOutbox: func(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
				return nil, nil
			},
			SchedulerDefinitions: func(context.Context, principalmodel.Principal) ([]recordmodel.Record, error) {
				return []recordmodel.Record{{ID: "nightly"}}, nil
			},
			SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
				return schema
			},
			SchemaObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
				return map[string]definitionmodel.ObjectSchema{}
			},
			ListRecords: func(_ context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
				return recordmodel.RecordPageResult{Total: 7, Items: []recordmodel.Record{{ID: objectKey + "-1"}}}, nil
			},
		},
	}
}

func TestBusinessSystemSnapshotSeparatesAdministratorAndLimitedVisibility(t *testing.T) {
	service := NewBusinessSystemApplicationService(businessSystemTestDependencies())
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	snapshot, err := service.Snapshot(t.Context(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaHash == "" || snapshot.SchemaHash != snapshot.Schema.SchemaHash || snapshot.Schema.SnapshotVersion != snapshot.SchemaHash || snapshot.ObjectRecordCounts["customer"] != 7 || len(snapshot.Schema.Workflows) != 1 || snapshot.Schema.Workflows[0].Key != "approval" {
		t.Fatalf("administrator snapshot=%#v", snapshot)
	}
	if snapshot.ResourceVisibility["runtime_state.integrations"] != "visible" || snapshot.FrontendCapabilities.Status != "ready" || snapshot.AuthoringContractHash == "" {
		t.Fatalf("administrator governance projection=%#v", snapshot)
	}

	limited := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workflow.definition.read"}})
	snapshot, err = service.Snapshot(t.Context(), limited)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ResourceVisibility["runtime_state.integrations"] != "hidden" {
		t.Fatalf("limited snapshot visibility=%#v", snapshot.ResourceVisibility)
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

func TestBusinessSystemObjectProjectionUsesSchemaBoundaryAndStableQuery(t *testing.T) {
	dependencies := businessSystemTestDependencies()
	service := NewBusinessSystemApplicationService(dependencies)
	if !service.RuntimeProjectionConfigured() || (*BusinessSystemApplicationService)(nil).RuntimeProjectionConfigured() {
		t.Fatal("runtime projection configuration check is incorrect")
	}
	missing, err := service.SnapshotObjectRecords(t.Context(), "job_run", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, 10)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing object records=%#v err=%v", missing, err)
	}

	var captured recordmodel.RecordListQuery
	dependencies.Runtime.SchemaObjectMap = func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"job_run": {Key: "job_run"}}
	}
	dependencies.Runtime.ListRecords = func(_ context.Context, _ string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		captured = query
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "run-1"}}}, nil
	}
	service = NewBusinessSystemApplicationService(dependencies)
	records, err := service.SnapshotObjectRecords(t.Context(), "job_run", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, 25)
	if err != nil || len(records) != 1 || records[0].ID != "run-1" {
		t.Fatalf("records=%#v err=%v", records, err)
	}
	if captured.Page != 1 || captured.PageSize != 25 || len(captured.Sort) != 1 || captured.Sort[0].Field != "updated_at" || captured.Sort[0].Direction != "desc" {
		t.Fatalf("query=%#v", captured)
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
			deps.Runtime.ConnectorCatalog = func(context.Context, principalmodel.Principal) ([]integrationmodel.ConnectorSchema, error) {
				return nil, want
			}
		}},
		{name: "connections", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.IntegrationConnections = func(context.Context, principalmodel.Principal) ([]integrationmodel.IntegrationConnection, error) {
				return nil, want
			}
		}},
		{name: "outbox", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Runtime.IntegrationOutbox = func(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
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

func TestBusinessSystemResourceAndFrontendProjectionPreservesEvidence(t *testing.T) {
	dependencies := businessSystemTestDependencies()
	returned := false
	dependencies.ApplicationDefinitions = func(context.Context, string, string, principalmodel.Principal) ([]appschemamodel.ApplicationDefinition, error) {
		if returned {
			return nil, nil
		}
		returned = true
		return []appschemamodel.ApplicationDefinition{{ResourceType: "object", ResourceKey: "customer", Name: "Customer", DisabledAt: "now"}}, nil
	}
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

	frontend := businessSystemFrontendCapabilities(deploymentmodel.FrontendCapabilitySnapshot{
		Revision: 3, Status: "ready",
		MissingFrontendSupport: []deploymentmodel.FrontendCapabilityRequirement{{CapabilityKey: "record.list", SupportKey: "record.table"}},
		StaleFrontendSupport:   []deploymentmodel.FrontendCapabilitySupportEntry{{SupportKey: "legacy"}},
		Manifest: &deploymentmodel.FrontendCapabilityManifest{
			ManifestVersion: "v1", FrontendVersion: "frontend-1", RuntimeContractVersions: []string{"runtime-1"},
			Entries:            []deploymentmodel.FrontendCapabilitySupportEntry{{SupportKey: "record.table", CapabilityKeys: []string{"record.list"}, RequiredPermissions: []string{"customer.read"}}},
			DeploymentEvidence: &deploymentmodel.FrontendDeploymentEvidence{AuditContractVersion: "audit-v1", DesignContractHash: "design", RouteRegistryHash: "routes", FrontendSourceHash: "source", AuditArtifactHash: "artifact"},
		},
	})
	if frontend.Manifest == nil || frontend.Manifest.DeploymentEvidence == nil || frontend.Manifest.DeploymentEvidence.FrontendSourceHash != "source" || len(frontend.MissingFrontendSupport) != 1 || len(frontend.StaleFrontendSupport) != 1 {
		t.Fatalf("frontend=%#v", frontend)
	}
	service.SetEvidenceRepository(nil)
	(*BusinessSystemApplicationService)(nil).SetEvidenceRepository(nil)
}

func TestBusinessSystemResourceProjectionPropagatesMetadataFailure(t *testing.T) {
	want := errors.New("metadata unavailable")
	dependencies := businessSystemTestDependencies()
	dependencies.ApplicationDefinitions = func(context.Context, string, string, principalmodel.Principal) ([]appschemamodel.ApplicationDefinition, error) {
		return nil, want
	}
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
			deps.ApplicationDefinitions = func(context.Context, string, string, principalmodel.Principal) ([]appschemamodel.ApplicationDefinition, error) {
				return nil, want
			}
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
		{name: "frontend", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.FrontendSnapshot = func(context.Context, principalmodel.Principal) (deploymentmodel.FrontendCapabilitySnapshot, error) {
				return deploymentmodel.FrontendCapabilitySnapshot{}, want
			}
		}},
		{name: "seed evidence", mutate: func(deps *BusinessSystemApplicationDependencies) {
			deps.Evidence = businessSystemEvidenceStub{err: want}
		}},
	}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
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
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	dependencies := businessSystemTestDependencies()
	dependencies.Evidence = businessSystemEvidenceStub{values: []businessseedmodel.BusinessSeedProvenance{{}}}
	dependencies.Runtime.IdempotencyStatus = func(context.Context, string) (deploymentmodel.IdempotencyOperationalStatus, error) {
		return deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{"ready": 1}}, nil
	}
	service := NewBusinessSystemApplicationService(dependencies)
	if snapshot, err := service.Snapshot(t.Context(), principal); err != nil || len(snapshot.SeedRecords) != 1 || snapshot.RuntimeState.Idempotency.Backlog["ready"] != 1 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}

	frontend := businessSystemFrontendCapabilities(deploymentmodel.FrontendCapabilitySnapshot{Manifest: &deploymentmodel.FrontendCapabilityManifest{ManifestVersion: "v1"}})
	if frontend.Manifest == nil || frontend.Manifest.DeploymentEvidence != nil {
		t.Fatalf("frontend=%+v", frontend)
	}

}
