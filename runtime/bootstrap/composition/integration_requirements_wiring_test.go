package composition

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestIntegrationEventMappingRequirementsBindsInstallationWorkspace(t *testing.T) {
	previous := principalmodel.InstallationWorkspaceID
	principalmodel.InstallationWorkspaceID = "workspace-installation"
	t.Cleanup(func() { principalmodel.InstallationWorkspaceID = previous })
	result := IntegrationEventMappingRequirements([]appschemamodel.IntegrationEventMappingSchema{{
		Key: "calendar-event", Provider: "calendar", EventType: "calendar.changed", TargetType: "automation",
		AutomationRuleKey: "meeting.webhook", AutomationInput: map[string]string{"title": "meeting.title"}, Enabled: true,
	}})
	if len(result) != 1 || result[0].WorkspaceID != "workspace-installation" || result[0].AutomationRuleKey != "meeting.webhook" || result[0].AutomationInput["title"] != "meeting.title" {
		t.Fatalf("requirements=%#v", result)
	}
}
