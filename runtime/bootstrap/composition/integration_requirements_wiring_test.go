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
		Key: "calendar-event", Provider: "calendar", EventType: "calendar.changed", TargetType: "action",
		ObjectKey: "meeting", ActionKey: "meeting.ingest", Enabled: true,
	}})
	if len(result) != 1 || result[0].WorkspaceID != "workspace-installation" {
		t.Fatalf("requirements=%#v", result)
	}
}
