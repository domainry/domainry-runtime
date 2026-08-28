package record

import (
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func requireRecordWorkspaceID(value string) (string, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(value)
	if err != nil {
		return "", fmt.Errorf("record workspace: %w", err)
	}
	return workspaceID.String(), nil
}
