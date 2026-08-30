package appschema

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestAgentExecutionDefinitionsAreNotRuntimeOwned(t *testing.T) {
	store := openStoreForMetadataTest(t)
	manifest := manifestmodel.ManifestSchema{
		TemplateID: "agent-runtime", Version: "1.0.0", Name: "Agent Runtime",
		Objects: []definitionmodel.ObjectSchema{{Key: "candidate", Name: "Candidate"}},
		AgentTasks: []agentsdk.AgentTaskDefinition{{
			ContractVersion: "agent-task-v1", Key: "recruiting.screen", Version: "1.0.0", Name: "Screen", AgentKey: "recruiter",
			Instruction: "Screen candidate", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
			AllowedOutcomes: []string{"success"}, SideEffectMode: agentsdk.AgentTaskSideEffectAnalysisOnly, Enabled: true,
		}},
		AgentEntrypoints:       []agentsdk.AgentEntrypointAssignment{{ContractVersion: "agent-entrypoint-v1", Key: "recruiting.global", AgentKey: "recruiter", Surface: "business_workspace", Enabled: true}},
		AgentServicePrincipals: []agentsdk.AgentServicePrincipalBinding{{ContractVersion: "agent-service-principal-v1", Key: "recruiting.service", UserID: "agent-user", RoleKey: "agent_service", Enabled: true, RotationVersion: 1}},
	}
	if err := store.EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	restored, err := store.LoadManifestMetadata(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.AgentTasks) != 0 || len(restored.AgentEntrypoints) != 0 || len(restored.AgentServicePrincipals) != 0 {
		t.Fatalf("Runtime AppSchema restored Agent-owned definitions: %#v", restored)
	}
	for _, table := range []string{"_agent_skill_definitions", "_agent_definitions", "_agent_task_definitions", "_agent_entrypoint_definitions", "_agent_service_principal_definitions"} {
		var count int
		if err := store.raw.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("Runtime AppSchema created Agent-owned table %s", table)
		}
	}
}
