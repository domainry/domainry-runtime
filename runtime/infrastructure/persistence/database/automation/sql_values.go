package automation

import (
	"fmt"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func automationWorkspaceID(value string) (string, error) {
	workspace, err := principalmodel.NewWorkspaceID(value)
	if err != nil {
		return "", err
	}
	return workspace.String(), nil
}

func automationExecutionWorkspaceID(workspaceID, embeddedWorkspaceID string) (string, error) {
	workspaceID, err := automationWorkspaceID(workspaceID)
	if err != nil {
		return "", err
	}
	if embeddedWorkspaceID = strings.TrimSpace(embeddedWorkspaceID); embeddedWorkspaceID != "" && embeddedWorkspaceID != workspaceID {
		return "", fmt.Errorf("automation execution workspace does not match repository workspace")
	}
	return workspaceID, nil
}

func stringsJoinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, store.Identifier(column))
	}
	return strings.Join(values, ", ")
}

func stringsJoinPlaceholders(store *database.RuntimeStore, count int) string {
	values := make([]string, 0, count)
	for position := 1; position <= count; position++ {
		values = append(values, store.Placeholder(position))
	}
	return strings.Join(values, ", ")
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
