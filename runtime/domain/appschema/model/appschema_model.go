package appschemamodel

import (
	"encoding/json"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
)

type ApplicationSchemaMigrationStep struct {
	ObjectKey   string `json:"object_key"`
	Table       string `json:"table"`
	Operation   string `json:"operation"`
	ColumnKey   string `json:"column_key,omitempty"`
	ColumnType  string `json:"column_type,omitempty"`
	Reversible  bool   `json:"reversible"`
	Description string `json:"description"`
}

type ApplicationSchemaPhysicalSchemaMismatchError struct {
	ObjectKey    string
	ColumnKey    string
	ExpectedType string
	ActualType   string
}

// ApplicationDefinition remains as a source-compatible alias while Metadata
// SDK owns the canonical business DTO.
type ApplicationDefinition = metadatasdk.Definition

func (e *ApplicationSchemaPhysicalSchemaMismatchError) Error() string {
	return "backend.metadata.physical_schema_incompatible"
}

func (e *ApplicationSchemaPhysicalSchemaMismatchError) ErrorCode() string {
	return e.Error()
}

func (e *ApplicationSchemaPhysicalSchemaMismatchError) ErrorParams() map[string]string {
	return map[string]string{
		"object_key":    e.ObjectKey,
		"column_key":    e.ColumnKey,
		"expected_type": e.ExpectedType,
		"actual_type":   e.ActualType,
	}
}

type ApplicationDefinitionUpsertRequest struct {
	ObjectKey string          `json:"object_key,omitempty"`
	Name      string          `json:"name,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

type ApplicationDefinitionMutation struct {
	Operation    string
	ResourceType string
	ResourceKey  string
	Request      ApplicationDefinitionUpsertRequest
}

// ApplicationSchemaUpgradePlanContractVersion identifies the JSON shape of a
// definition upgrade plan printed by DEFINITION_UPGRADE_MODE=plan.
const ApplicationSchemaUpgradePlanContractVersion = "domainry-definition-upgrade-plan-v1"

// Upgrade step classifications.
const (
	ApplicationSchemaUpgradeCompatible    = "compatible"
	ApplicationSchemaUpgradeRequiresRule  = "requires_rule"
	ApplicationSchemaUpgradeDataDependent = "data_dependent"
	ApplicationSchemaUpgradeIncompatible  = "incompatible"
	ApplicationSchemaUpgradeRetained      = "retained"
)

// Upgrade step and diagnostic error codes.
const (
	ApplicationSchemaUpgradeRequiredFieldRuleMissingCode = "backend.metadata.upgrade_required_field_rule_missing"
	ApplicationSchemaUpgradeUniqueConflictCode           = "backend.metadata.upgrade_unique_conflict"
	ApplicationSchemaUpgradeBackupUnavailableCode        = "backend.metadata.upgrade_backup_unavailable"
	ApplicationSchemaRetainedMigrationDescription        = "backend.metadata.migration.retained"
)

// ApplicationSchemaUpgradeStep is one physical change between two definition
// versions together with its compatibility classification.
type ApplicationSchemaUpgradeStep struct {
	ApplicationSchemaMigrationStep
	Classification string            `json:"classification"`
	Blocking       bool              `json:"blocking"`
	ErrorCode      string            `json:"error_code,omitempty"`
	Params         map[string]string `json:"params,omitempty"`
	ExistingRows   int64             `json:"existing_rows,omitempty"`
}

// ApplicationSchemaUpgradeDiagnostic is a non-step observation about the
// upgrade, such as an unavailable backup.
type ApplicationSchemaUpgradeDiagnostic struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
}

// ApplicationSchemaUpgradePlan is the complete evaluation of moving the
// physical schema from one definition version to the next.
type ApplicationSchemaUpgradePlan struct {
	ContractVersion string                               `json:"contract_version"`
	FromVersion     string                               `json:"from_version"`
	ToVersion       string                               `json:"to_version"`
	Blocking        bool                                 `json:"blocking"`
	Steps           []ApplicationSchemaUpgradeStep       `json:"steps"`
	Diagnostics     []ApplicationSchemaUpgradeDiagnostic `json:"diagnostics"`
}

// PendingSteps returns the steps that change the physical schema; retained
// tables and columns are informational only.
func (p ApplicationSchemaUpgradePlan) PendingSteps() []ApplicationSchemaUpgradeStep {
	pending := []ApplicationSchemaUpgradeStep{}
	for _, step := range p.Steps {
		if step.Classification == ApplicationSchemaUpgradeRetained {
			continue
		}
		pending = append(pending, step)
	}
	return pending
}

// BlockingSteps returns the steps that prevent the upgrade from being applied.
func (p ApplicationSchemaUpgradePlan) BlockingSteps() []ApplicationSchemaUpgradeStep {
	blocking := []ApplicationSchemaUpgradeStep{}
	for _, step := range p.Steps {
		if step.Blocking {
			blocking = append(blocking, step)
		}
	}
	return blocking
}
