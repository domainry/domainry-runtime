package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestIntegrationConnectionRequirementsBindsInstallationWorkspace(t *testing.T) {
	previous := principalmodel.InstallationWorkspaceID
	principalmodel.InstallationWorkspaceID = "workspace-installation"
	t.Cleanup(func() { principalmodel.InstallationWorkspaceID = previous })
	config := json.RawMessage(`{"project_id":"facility-prod"}`)
	result := IntegrationConnectionRequirements([]integrationsdk.ConnectionRequirement{{
		Key: "facility_fcm", ConnectorKey: "notification", ProviderKey: "fcm", Config: config,
	}})
	config[0] = '['
	if len(result) != 1 || result[0].WorkspaceID != "workspace-installation" || string(result[0].Config) != `{"project_id":"facility-prod"}` {
		t.Fatalf("requirements=%#v", result)
	}
}

func TestSynchronizeWorkspaceIntegrationEventMappingsBindsOnlyRequestedWorkspace(t *testing.T) {
	mappings := []integrationsdk.EventMappingRequirement{{
		Key: "calendar-event", Provider: "calendar", EventType: "calendar.changed", TargetType: "automation",
		ConnectionKey: "scoped-calendar", AutomationRuleKey: "meeting.webhook",
		AutomationInput: map[string]string{"title": "meeting.title"}, Enabled: true,
	}}
	probe := &mappingPublicationProbe{}
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		if err := SynchronizeWorkspaceIntegrationEventMappings(t.Context(), probe, mappings, workspace); err != nil {
			t.Fatal(err)
		}
		result := probe.snapshots[len(probe.snapshots)-1]
		if len(result) != 1 || result[0].WorkspaceID != workspace || result[0].ConnectionKey != "scoped-calendar" ||
			result[0].AutomationRuleKey != "meeting.webhook" || result[0].AutomationInput["title"] != "meeting.title" {
			t.Fatalf("requirements=%#v", result)
		}
	}
	if mappings[0].WorkspaceID != "" || probe.snapshots[0][0].WorkspaceID != "workspace-a" {
		t.Fatal("publishing the second workspace changed the source or the first workspace")
	}
	if err := SynchronizeWorkspaceIntegrationEventMappings(t.Context(), probe, mappings, ""); err == nil || len(probe.snapshots) != 2 {
		t.Fatal("an invalid workspace must be rejected before calling Integration")
	}
	probe.err = errors.New("Integration unavailable")
	if err := SynchronizeWorkspaceIntegrationEventMappings(t.Context(), probe, mappings, "workspace-b"); !errors.Is(err, probe.err) {
		t.Fatalf("expected publication error, got %v", err)
	}
}

type mappingPublicationProbe struct {
	integrationsdk.Requirements
	snapshots [][]integrationsdk.EventMappingRequirement
	err       error
}

func (probe *mappingPublicationProbe) SynchronizeEventMappings(_ context.Context, values []integrationsdk.EventMappingRequirement) error {
	probe.snapshots = append(probe.snapshots, values)
	return probe.err
}
