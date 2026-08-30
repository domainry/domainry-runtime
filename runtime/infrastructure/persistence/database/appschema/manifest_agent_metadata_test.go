package appschema

import (
	"testing"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestAgentExecutionDefinitionsAreDurable(t *testing.T) {
	store := openStoreForMetadataTest(t)
	manifest := manifestmodel.ManifestSchema{
		TemplateID: "agent-runtime", Version: "1.0.0", Name: "Agent Runtime",
		Objects: []definitionmodel.ObjectSchema{{Key: "candidate", Name: "Candidate"}},
		AgentTasks: []agentmodel.AgentTaskDefinition{{
			ContractVersion: "agent-task-v1", Key: "recruiting.screen", Version: "1.0.0", Name: "Screen", AgentKey: "recruiter",
			Instruction: "Screen candidate", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
			AllowedOutcomes: []string{"success"}, SideEffectMode: agentmodel.AgentTaskSideEffectAnalysisOnly, Enabled: true,
		}},
		AgentEntrypoints:       []agentmodel.AgentEntrypointAssignment{{ContractVersion: "agent-entrypoint-v1", Key: "recruiting.global", AgentKey: "recruiter", Surface: "business_workspace", Enabled: true}},
		AgentServicePrincipals: []agentmodel.AgentServicePrincipalBinding{{ContractVersion: "agent-service-principal-v1", Key: "recruiting.service", UserID: "agent-user", RoleKey: "agent_service", Enabled: true, RotationVersion: 1}},
	}
	if err := store.EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	restored, err := store.LoadManifestMetadata(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.AgentTasks) != 1 || restored.AgentTasks[0].Key != "recruiting.screen" || restored.AgentTasks[0].Version != "1.0.0" {
		t.Fatalf("agent tasks were not restored: %#v", restored.AgentTasks)
	}
	if len(restored.AgentEntrypoints) != 1 || restored.AgentEntrypoints[0].Key != "recruiting.global" {
		t.Fatalf("agent entrypoints were not restored: %#v", restored.AgentEntrypoints)
	}
	if len(restored.AgentServicePrincipals) != 1 || restored.AgentServicePrincipals[0].Key != "recruiting.service" {
		t.Fatalf("agent service principals were not restored: %#v", restored.AgentServicePrincipals)
	}
	for table, want := range map[string]int{"agent_task_definitions": 1, "agent_entrypoint_definitions": 1, "agent_service_principal_definitions": 1} {
		var count int
		if err := store.raw.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d", table, count, want)
		}
	}
}
