package appschema

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

// Definition upgrade modes selected by DEFINITION_UPGRADE_MODE.
const (
	DefinitionUpgradeModeApply  = "apply"
	DefinitionUpgradeModeVerify = "verify"
	DefinitionUpgradeModePlan   = "plan"
)

// Definition upgrade error codes raised by Runtime metadata restoration.
const (
	DefinitionUpgradePendingCode     = "backend.metadata.definition_upgrade_pending"
	DefinitionUpgradeBlockedCode     = "backend.metadata.definition_upgrade_blocked"
	DefinitionUpgradeModeInvalidCode = "backend.metadata.definition_upgrade_mode_invalid"
	DefinitionUpgradePlanCode        = "backend.metadata.definition_upgrade_plan"
)

// DefinitionUpgradePlanRequested is returned by Restore in plan mode: the
// database was not modified and the caller must print the plan and stop.
type DefinitionUpgradePlanRequested struct {
	Plan appschemamodel.ApplicationSchemaUpgradePlan
}

func (e *DefinitionUpgradePlanRequested) Error() string { return DefinitionUpgradePlanCode }

func (e *DefinitionUpgradePlanRequested) ErrorCode() string { return DefinitionUpgradePlanCode }

func (e *DefinitionUpgradePlanRequested) ErrorParams() map[string]string {
	return map[string]string{"from_version": e.Plan.FromVersion, "to_version": e.Plan.ToVersion}
}

// PlanJSON renders the plan as one JSON document.
func (e *DefinitionUpgradePlanRequested) PlanJSON() ([]byte, error) {
	return json.Marshal(e.Plan)
}

func normalizeDefinitionUpgradeMode(mode string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(mode)); normalized {
	case "":
		return DefinitionUpgradeModeApply, nil
	case DefinitionUpgradeModeApply, DefinitionUpgradeModeVerify, DefinitionUpgradeModePlan:
		return normalized, nil
	default:
		return "", &apperror.AppError{Kind: apperror.KindBadRequest, Code: DefinitionUpgradeModeInvalidCode, Params: map[string]string{"mode": strings.TrimSpace(mode)}}
	}
}

// definitionUpgradeError carries the plan JSON in the error message so the
// startup log shows exactly which steps are pending or blocking.
func definitionUpgradeError(code string, plan appschemamodel.ApplicationSchemaUpgradePlan) error {
	payload, err := json.Marshal(plan)
	if err != nil {
		payload = []byte(`{"error":"plan could not be encoded"}`)
	}
	params := map[string]string{"from_version": plan.FromVersion, "to_version": plan.ToVersion, "plan": string(payload)}
	summary := []string{}
	for _, step := range plan.BlockingSteps() {
		target := step.ObjectKey
		if step.ColumnKey != "" {
			target += "." + step.ColumnKey
		}
		summary = append(summary, step.ErrorCode+"("+target+")")
	}
	if len(summary) > 0 {
		params["blocking"] = strings.Join(summary, "; ")
	}
	base := &apperror.AppError{Kind: apperror.KindConflict, Code: code, Params: params}
	if len(summary) > 0 {
		return fmt.Errorf("%w: blocking=[%s]: %s", base, strings.Join(summary, "; "), payload)
	}
	return fmt.Errorf("%w: %s", base, payload)
}
