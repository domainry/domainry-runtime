package projection

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"testing"
)

func TestEntrypointProjectionsAcceptNilAndDoNotMutateInput(t *testing.T) {
	resolved := integrationmodel.IntegrationExternalIdentityResolveResult{
		Provider: "slack", ExternalPrincipal: "user:U1", ExternalSubjectType: "user",
		ActorID: "actor_1", RoleKey: "member", MappingKey: "mapping_1", Mapped: true,
	}

	payload := IntegrationEntrypointPayload(nil, resolved)
	if payload["external_principal"] != "user:U1" || payload["external_provider"] != "slack" {
		t.Fatalf("nil payload projection=%#v", payload)
	}
	metadata := IntegrationEntrypointAuditMetadata(resolved, nil)
	if metadata["actor_id"] != "actor_1" || metadata["external_mapped"] != true {
		t.Fatalf("nil audit metadata projection=%#v", metadata)
	}

	extra := map[string]any{"source": "proposal"}
	projected := IntegrationEntrypointAuditMetadata(resolved, extra)
	projected["source"] = "changed"
	if extra["source"] != "proposal" {
		t.Fatalf("input metadata mutated: %#v", extra)
	}
}

func TestIntegrationEntrypointPayloadIncludesOptionalExternalIdentityFields(t *testing.T) {
	input := map[string]any{"business": "value", "external_name": "stale"}
	resolved := integrationmodel.IntegrationExternalIdentityResolveResult{
		Provider: "slack", ExternalPrincipal: "user:U1", ExternalSubject: "U1", ExternalSubjectType: "user",
		ActorID: "actor_1", RoleKey: "member", ExternalName: "Alice", ExternalOrganization: "Acme",
		ExternalDepartment: "Sales", ExternalGroup: "East", ExternalBotID: "bot-1",
	}
	got := IntegrationEntrypointPayload(input, resolved)
	for key, want := range map[string]any{
		"business": "value", "external_name": "Alice", "external_organization": "Acme",
		"external_department": "Sales", "external_group": "East", "external_bot_id": "bot-1",
		"external_subject": "U1", "external_actor_id": "actor_1", "external_role_key": "member",
	} {
		if got[key] != want {
			t.Fatalf("payload[%q] = %#v, want %#v; payload=%#v", key, got[key], want, got)
		}
	}
	got["business"] = "changed"
	if input["business"] != "value" || input["external_name"] != "stale" {
		t.Fatalf("input payload mutated: %#v", input)
	}
}
