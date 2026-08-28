package workflow

import (
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func requireWorkflowWorkspaceID(value string) (string, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(value)
	if err != nil {
		return "", fmt.Errorf("workflow workspace: %w", err)
	}
	return workspaceID.String(), nil
}
