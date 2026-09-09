package appschema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func definitionUpgradeTestScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test definition upgrade")
}

func definitionUpgradeTestPlan(blocking bool) appschemamodel.ApplicationSchemaUpgradePlan {
	step := appschemamodel.ApplicationSchemaUpgradeStep{
		ApplicationSchemaMigrationStep: appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: "customer", Table: "customer", Operation: "add_column", ColumnKey: "tier"},
		Classification:                 appschemamodel.ApplicationSchemaUpgradeCompatible,
	}
	if blocking {
		step.Classification = appschemamodel.ApplicationSchemaUpgradeRequiresRule
		step.Blocking = true
		step.ErrorCode = appschemamodel.ApplicationSchemaUpgradeRequiredFieldRuleMissingCode
	}
	retained := appschemamodel.ApplicationSchemaUpgradeStep{
		ApplicationSchemaMigrationStep: appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: "archive", Table: "archive", Operation: "retain_table"},
		Classification:                 appschemamodel.ApplicationSchemaUpgradeRetained,
	}
	return appschemamodel.ApplicationSchemaUpgradePlan{ContractVersion: appschemamodel.ApplicationSchemaUpgradePlanContractVersion, Blocking: blocking, Steps: []appschemamodel.ApplicationSchemaUpgradeStep{step, retained}}
}

func TestRestorePlanModeReturnsPlanWithoutTouchingRepositories(t *testing.T) {
	previous := manifestmodel.ManifestSchema{Version: "1"}
	applied := appschemamodel.ApplicationSchemaUpgradePlan{}
	synced := manifestmodel.ManifestSchema{}
	metadata := runtimeMetadataRepositoryStub{previous: &previous, plan: definitionUpgradeTestPlan(false), applied: &applied, synced: &synced, ensureErr: errors.New("projection must not run")}
	service := NewApplicationSchemaRuntimeRestorationApplicationService(runtimeNotificationRepositoryStub{syncErr: errors.New("notifications must not run")}, metadata).WithDefinitionUpgradeMode(DefinitionUpgradeModePlan)
	_, err := service.Restore(t.Context(), manifestmodel.ManifestSchema{Version: "2"}, definitionUpgradeTestScope())
	var requested *DefinitionUpgradePlanRequested
	if !errors.As(err, &requested) || requested.Plan.FromVersion != "1" || requested.Plan.ToVersion != "2" || len(requested.Plan.Steps) != 2 {
		t.Fatalf("plan mode err=%v", err)
	}
	if applied.ToVersion != "" || synced.Version != "" {
		t.Fatalf("plan mode applied the upgrade: applied=%+v synced=%+v", applied, synced)
	}
	payload, encodeErr := requested.PlanJSON()
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	var decoded map[string]any
	if decodeErr := json.Unmarshal(payload, &decoded); decodeErr != nil || decoded["contract_version"] != appschemamodel.ApplicationSchemaUpgradePlanContractVersion || decoded["to_version"] != "2" {
		t.Fatalf("plan json=%s err=%v", payload, decodeErr)
	}
	if apperror.CodeOf(apperror.FromError(apperror.KindConflict, err)) != DefinitionUpgradePlanCode {
		t.Fatalf("plan sentinel code=%v", err)
	}
}

func TestRestoreVerifyModeFailsOnPendingStepsButAcceptsRetainedOnly(t *testing.T) {
	scope := definitionUpgradeTestScope()
	pending := runtimeMetadataRepositoryStub{plan: definitionUpgradeTestPlan(false), manifest: manifestmodel.ManifestSchema{Version: "2"}}
	_, err := NewApplicationSchemaRuntimeRestorationApplicationService(runtimeNotificationRepositoryStub{}, pending).WithDefinitionUpgradeMode(DefinitionUpgradeModeVerify).Restore(t.Context(), manifestmodel.ManifestSchema{Version: "2"}, scope)
	if apperror.CodeOf(err) != DefinitionUpgradePendingCode || !strings.Contains(err.Error(), `"operation":"add_column"`) || apperror.ParamsOf(err)["to_version"] != "2" {
		t.Fatalf("verify pending err=%v params=%v", err, apperror.ParamsOf(err))
	}
	retainedOnly := definitionUpgradeTestPlan(false)
	retainedOnly.Steps = retainedOnly.Steps[1:]
	applied := appschemamodel.ApplicationSchemaUpgradePlan{ToVersion: "untouched"}
	clean := runtimeMetadataRepositoryStub{plan: retainedOnly, manifest: manifestmodel.ManifestSchema{Version: "2"}, applied: &applied}
	if _, err := NewApplicationSchemaRuntimeRestorationApplicationService(runtimeNotificationRepositoryStub{}, clean).WithDefinitionUpgradeMode(DefinitionUpgradeModeVerify).Restore(t.Context(), manifestmodel.ManifestSchema{Version: "2"}, scope); err != nil {
		t.Fatalf("verify with retained-only plan failed: %v", err)
	}
	if applied.ToVersion != "untouched" {
		t.Fatalf("verify mode applied the upgrade: %+v", applied)
	}
	blocking := runtimeMetadataRepositoryStub{plan: definitionUpgradeTestPlan(true), manifest: manifestmodel.ManifestSchema{Version: "2"}}
	if _, err := NewApplicationSchemaRuntimeRestorationApplicationService(runtimeNotificationRepositoryStub{}, blocking).WithDefinitionUpgradeMode(DefinitionUpgradeModeVerify).Restore(t.Context(), manifestmodel.ManifestSchema{Version: "2"}, scope); apperror.CodeOf(err) != DefinitionUpgradePendingCode {
		t.Fatalf("verify blocking err=%v", err)
	}
}

func TestRestoreApplyModeRefusesBlockingPlanAndAppliesCompatibleOne(t *testing.T) {
	scope := definitionUpgradeTestScope()
	applied := appschemamodel.ApplicationSchemaUpgradePlan{}
	blocking := runtimeMetadataRepositoryStub{plan: definitionUpgradeTestPlan(true), manifest: manifestmodel.ManifestSchema{Version: "2"}, applied: &applied, ensureErr: errors.New("projection must not run")}
	_, err := NewApplicationSchemaRuntimeRestorationApplicationService(runtimeNotificationRepositoryStub{}, blocking).Restore(t.Context(), manifestmodel.ManifestSchema{Version: "2"}, scope)
	if apperror.CodeOf(err) != DefinitionUpgradeBlockedCode || !strings.Contains(err.Error(), appschemamodel.ApplicationSchemaUpgradeRequiredFieldRuleMissingCode+"(customer.tier)") || apperror.ParamsOf(err)["blocking"] == "" {
		t.Fatalf("apply blocking err=%v", err)
	}
	if applied.ToVersion != "" {
		t.Fatalf("blocked plan was applied: %+v", applied)
	}
	synced := manifestmodel.ManifestSchema{}
	compatible := runtimeMetadataRepositoryStub{plan: definitionUpgradeTestPlan(false), manifest: manifestmodel.ManifestSchema{Version: "2"}, applied: &applied, synced: &synced}
	if _, err := NewApplicationSchemaRuntimeRestorationApplicationService(runtimeNotificationRepositoryStub{}, compatible).Restore(t.Context(), manifestmodel.ManifestSchema{Version: "2", Name: "installed"}, scope); err != nil {
		t.Fatal(err)
	}
	if applied.ToVersion != "2" || synced.Name != "installed" {
		t.Fatalf("compatible plan not applied: applied=%+v synced=%+v", applied, synced)
	}
	if _, err := NewApplicationSchemaRuntimeRestorationApplicationService(runtimeNotificationRepositoryStub{}, compatible).WithDefinitionUpgradeMode("rollback").Restore(t.Context(), manifestmodel.ManifestSchema{Version: "2"}, scope); apperror.CodeOf(err) != DefinitionUpgradeModeInvalidCode {
		t.Fatalf("invalid mode err=%v", err)
	}
}
