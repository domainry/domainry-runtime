package action

import (
	"context"
	"fmt"
	"strings"

	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type ActionPreconditionDependencies struct {
	PipelineStage func(context.Context, string, string) (recordmodel.Record, error)
}

type ActionPreconditionApplicationService struct {
	dependencies ActionPreconditionDependencies
}

func NewActionPreconditionApplicationService(dependencies ActionPreconditionDependencies) *ActionPreconditionApplicationService {
	return &ActionPreconditionApplicationService{dependencies: dependencies}
}

func (s *ActionPreconditionApplicationService) Check(ctx context.Context, action definitionmodel.ActionSchema, record recordmodel.Record, principal principalmodel.Principal) error {
	if err := actionpolicy.ActionCheckPreconditions(action, record.Data); err != nil {
		return err
	}
	for _, precondition := range action.Preconditions {
		if strings.TrimSpace(strings.ToLower(precondition)) != "current stage is terminal" {
			continue
		}
		stageID := actionpolicy.ActionNormalizedValue(record.Data["current_stage"])
		if stageID == "" || s.dependencies.PipelineStage == nil {
			return actionpolicy.ActionPreconditionFailedError()
		}
		stage, err := s.dependencies.PipelineStage(ctx, principal.WorkspaceID, stageID)
		if err != nil {
			return err
		}
		if !preconditionPipelineStageTerminal(stage) {
			return actionpolicy.ActionPreconditionFailedError()
		}
	}
	return nil
}

func preconditionPipelineStageTerminal(stage recordmodel.Record) bool {
	if value, ok := actionpolicy.ActionBoolValue(stage.Data["is_terminal"]); ok && value {
		return true
	}
	switch strings.TrimSpace(fmt.Sprint(stage.Data["stage_type"])) {
	case "won", "lost", "done", "cancelled", "failed":
		return true
	default:
		return false
	}
}
