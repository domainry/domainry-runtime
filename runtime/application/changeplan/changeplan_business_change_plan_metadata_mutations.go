package changeplan

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	changeplancontract "github.com/domainry/domainry-runtime/runtime/domain/changeplan/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"encoding/json"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func (s *ChangePlanApplicationService) metadataChangePlanMutations(ctx context.Context, plan changeplanmodel.BusinessSystemChangePlan, principal principalmodel.Principal) ([]appschemamodel.ApplicationDefinitionMutation, []auditmodel.AuditEvent, error) {
	items := map[string]changeplanmodel.BusinessSystemChangeItem{}
	for _, item := range plan.Items {
		items[item.ItemID] = item
	}
	mutations := []appschemamodel.ApplicationDefinitionMutation{}
	audits := []auditmodel.AuditEvent{}
	for _, itemID := range plan.ReleaseOrder {
		item := items[itemID]
		if item.Operation == "noop" {
			continue
		}
		resourceType := strings.TrimSpace(item.ResourceType)
		if !businessChangePlanMetadataResourceType(resourceType) {
			return nil, nil, badRequest("backend.change_plan.apply_resource_unsupported", "item_id", item.ItemID, "resource_type", resourceType, "resource_key", item.ResourceKey)
		}
		request := metadataChangePlanRequest(plan, item)
		mutations = append(mutations, appschemamodel.ApplicationDefinitionMutation{Operation: item.Operation, ResourceType: resourceType, ResourceKey: item.ResourceKey, Request: request})
		audits = append(audits, buildBusinessChangePlanAudit(plan, item, principal))
	}
	if s.runtime == nil {
		return nil, nil, internalError("validate composed metadata candidate", nil)
	}
	mutations, err := s.runtime.CanonicalizeMetadataCandidate(ctx, mutations)
	if err != nil {
		return nil, nil, err
	}
	return mutations, audits, nil
}

func metadataChangePlanRequest(plan changeplanmodel.BusinessSystemChangePlan, item changeplanmodel.BusinessSystemChangeItem) appschemamodel.ApplicationDefinitionUpsertRequest {
	expected := item.ExpectedResourceHash
	if item.Operation == "create" {
		expected = ""
	}
	after := businessChangeJSONMap(item.After)
	objectKey := strings.TrimSpace(stringValue(after["object_key"]))
	if objectKey == "" && item.ResourceType == "field" {
		objectKey = strings.SplitN(item.ResourceKey, ".", 2)[0]
	}
	name := strings.TrimSpace(stringValue(after["name"]))
	return appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: objectKey, Name: name, SourceKind: "builder", SourceID: plan.PlanID, ExpectedSchemaHash: &expected, Payload: append(json.RawMessage(nil), item.After...)}
}

func businessChangePlanMetadataResourceType(resourceType string) bool {
	resourceType = strings.TrimSpace(resourceType)
	for _, candidate := range businessChangePlanMetadataResourceTypes() {
		if resourceType == candidate {
			return true
		}
	}
	return false
}

func businessChangePlanMetadataResourceTypes() []string {
	return []string{"object", "field", "validation", "view", "action", "workflow", "scheduler", "automation_rule", "preference", "rule_set", "dictionary", "connector", "integration_event_mapping", "report", "operation_state_example", "sensitive_field_policy", "report_export_control", "role", "identity_profile_binding", "entrypoint", "skill", "agent"}
}

func buildBusinessChangePlanAudit(plan changeplanmodel.BusinessSystemChangePlan, item changeplanmodel.BusinessSystemChangeItem, principal principalmodel.Principal) auditmodel.AuditEvent {
	metadata := map[string]any{"plan_id": plan.PlanID, "builder_task_id": plan.BuilderTaskID, "business_reason": plan.BusinessReason, "operation": item.Operation, "change_kind": item.ChangeKind, "risk_level": item.RiskLevel, "capability_key": item.CapabilityKey, "validation_result": "passed", "validation_methods": item.ValidationMethods, "snapshot_hash": plan.SnapshotHash, "reference_graph_hash": plan.ReferenceGraphHash, "runtime_version": plan.RuntimeVersion, "contract_version": plan.AuthoringContractVersion, "contract_hash": plan.AuthoringContractHash, "frontend_manifest_version": plan.FrontendManifestVersion, "frontend_manifest_hash": plan.FrontendManifestHash, "reviewed": plan.Reviewed, "reviewed_by": plan.ReviewedBy, "reference_migrations": item.ReferenceMigrations, "rollback_method": item.RollbackMethod}
	return buildAuditEvent("business_change_plan.item_applied", item.ResourceType, item.ResourceKey, principal, changeplanprojection.ChangePlanBusinessSummary(item), businessChangeJSONMap(item.Before), businessChangeJSONMap(item.After), metadata)
}

func businessChangeJSONMap(payload json.RawMessage) map[string]any {
	values := map[string]any{}
	_ = json.Unmarshal(payload, &values)
	return values
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

// ImportPackageDraft binds a current-contract system package to the target
// workspace's current immutable snapshot and reference graph. Import only
// creates a normal editable draft; review, independent approval and atomic
// publish remain mandatory and use the same lifecycle as human/model authoring.
func (s *ChangePlanApplicationService) ImportPackageDraft(ctx context.Context, planID, businessReason string, expectedRevision int, systemPackage changeplanmodel.BusinessSystemPackage, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, error) {
	if err := changePlanAuthorizeCommand(principal); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return changeplanmodel.BusinessChangePlanDraft{}, forbidden("auth.permission_denied")
	}
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return changeplanmodel.BusinessChangePlanDraft{}, badRequest("backend.change_plan.plan_id_required")
	}
	snapshotValue, graphValue := snapshot.ChangePlanSnapshot(), graph.ChangePlanReferenceGraph()
	if err := validateBusinessSystemPackage(systemPackage, snapshotValue); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, err
	}
	items := make([]changeplanmodel.BusinessSystemChangeItem, 0, len(systemPackage.Resources))
	itemByResource := make(map[string]int, len(systemPackage.Resources))
	for _, resource := range systemPackage.Resources {
		itemID := packageResourceIdentity(resource.ResourceType, resource.ResourceKey)
		itemByResource[itemID] = len(items)
		items = append(items, changeplanmodel.BusinessSystemChangeItem{ItemID: itemID, Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: strings.TrimSpace(resource.ResourceType), ResourceKey: strings.TrimSpace(resource.ResourceKey), ResourceOwner: "builder", OwnerAuthorized: true, CapabilityKey: "maintenance.change_plan_validation", After: append(json.RawMessage(nil), resource.Payload...), ValidationMethods: []string{"metadata.validate", "package.resource_hash"}, RollbackMethod: "delete_created_resource"})
	}
	for _, dependency := range systemPackage.Dependencies {
		index, ok := itemByResource[packageResourceIdentity(dependency.FromResourceType, dependency.FromResourceKey)]
		if !ok {
			return changeplanmodel.BusinessChangePlanDraft{}, badRequest("backend.change_plan.package_dependency_invalid", "resource_type", dependency.FromResourceType, "resource_key", dependency.FromResourceKey)
		}
		items[index].Dependencies = append(items[index].Dependencies, changeplanmodel.BusinessChangeTarget{ResourceType: strings.TrimSpace(dependency.ToResourceType), ResourceKey: strings.TrimSpace(dependency.ToResourceKey), Reason: strings.TrimSpace(dependency.Reason)})
	}
	releaseOrder, err := packageReleaseOrder(items)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, err
	}
	rollbackOrder := append([]string(nil), releaseOrder...)
	for left, right := 0, len(rollbackOrder)-1; left < right; left, right = left+1, right-1 {
		rollbackOrder[left], rollbackOrder[right] = rollbackOrder[right], rollbackOrder[left]
	}
	if strings.TrimSpace(businessReason) == "" {
		businessReason = "Import current-contract system package " + systemPackage.PackageHash
	}
	plan := changeplanmodel.BusinessSystemChangePlan{PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: planID, BusinessReason: strings.TrimSpace(businessReason), BuilderTaskID: "package:" + systemPackage.PackageHash, SnapshotHash: snapshotValue.SnapshotHash, ReferenceGraphHash: graphValue.Hash, RuntimeVersion: snapshotValue.RuntimeVersion, AuthoringContractVersion: snapshotValue.AuthoringContractVersion, AuthoringContractHash: snapshotValue.AuthoringContractHash, ReleaseOrder: releaseOrder, RollbackOrder: rollbackOrder, AcceptanceScenarios: cloneChangePlanScenarios(systemPackage.AcceptanceScenarios), Items: items}
	return s.SaveDraft(ctx, plan, expectedRevision, principal)
}

func validateBusinessSystemPackage(systemPackage changeplanmodel.BusinessSystemPackage, snapshot changeplanmodel.Snapshot) error {
	if systemPackage.PackageVersion != changeplanmodel.BusinessSystemPackageVersion {
		return badRequest("backend.change_plan.package_version_unsupported", "actual", systemPackage.PackageVersion, "expected", changeplanmodel.BusinessSystemPackageVersion)
	}
	if systemPackage.RuntimeVersion != snapshot.RuntimeVersion || systemPackage.AuthoringContractVersion != snapshot.AuthoringContractVersion || systemPackage.AuthoringContractHash != snapshot.AuthoringContractHash {
		return conflict("backend.change_plan.package_contract_mismatch", "package_runtime_version", systemPackage.RuntimeVersion, "runtime_version", snapshot.RuntimeVersion)
	}
	if systemPackage.EmptyWorkspaceApplyPlan.Target != "empty_workspace" || !systemPackage.EmptyWorkspaceApplyPlan.RequiresSnapshotBinding || !systemPackage.EmptyWorkspaceApplyPlan.RequiresReferenceBinding {
		return badRequest("backend.change_plan.package_apply_plan_invalid")
	}
	if hashBusinessSystemPackage(systemPackage) != strings.TrimSpace(systemPackage.PackageHash) {
		return badRequest("backend.change_plan.package_hash_mismatch")
	}
	existing := make(map[string]bool, len(snapshot.ResourceSources))
	for _, source := range snapshot.ResourceSources {
		existing[packageResourceIdentity(source.ResourceType, source.ResourceKey)] = true
	}
	resources := make(map[string]changeplanmodel.BusinessSystemPackageResource, len(systemPackage.Resources))
	for _, resource := range systemPackage.Resources {
		identity := packageResourceIdentity(resource.ResourceType, resource.ResourceKey)
		_, duplicate := resources[identity]
		if strings.TrimSpace(resource.ResourceType) == "" || strings.TrimSpace(resource.ResourceKey) == "" || !businessChangePlanMetadataResourceType(strings.TrimSpace(resource.ResourceType)) || duplicate {
			return badRequest("backend.change_plan.package_resource_invalid", "resource_type", resource.ResourceType, "resource_key", resource.ResourceKey)
		}
		payload, hash, err := canonicalPackagePayload(resource.Payload)
		if err != nil || hash != strings.TrimSpace(resource.ResourceHash) {
			return badRequest("backend.change_plan.package_resource_hash_mismatch", "resource_type", resource.ResourceType, "resource_key", resource.ResourceKey)
		}
		resource.Payload = payload
		resources[identity] = resource
		if existing[identity] {
			return conflict("backend.change_plan.package_target_not_empty", "resource_type", resource.ResourceType, "resource_key", resource.ResourceKey)
		}
	}
	if len(resources) == 0 || len(systemPackage.EmptyWorkspaceApplyPlan.Operations) != len(resources) {
		return badRequest("backend.change_plan.package_apply_plan_invalid")
	}
	seenOperations := make(map[string]bool, len(resources))
	for _, operation := range systemPackage.EmptyWorkspaceApplyPlan.Operations {
		identity := packageResourceIdentity(operation.ResourceType, operation.ResourceKey)
		resource, ok := resources[identity]
		operationPayload, _, err := canonicalPackagePayload(operation.Payload)
		if operation.Operation != "create" || !ok || seenOperations[identity] || err != nil || string(operationPayload) != string(resource.Payload) {
			return badRequest("backend.change_plan.package_apply_plan_invalid", "resource_type", operation.ResourceType, "resource_key", operation.ResourceKey)
		}
		seenOperations[identity] = true
	}
	return nil
}

func hashBusinessSystemPackage(systemPackage changeplanmodel.BusinessSystemPackage) string {
	systemPackage.PackageHash = ""
	raw, _ := json.Marshal(systemPackage)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func packageResourceIdentity(resourceType, resourceKey string) string {
	return strings.TrimSpace(resourceType) + ":" + strings.TrimSpace(resourceKey)
}

func packageReleaseOrder(items []changeplanmodel.BusinessSystemChangeItem) ([]string, error) {
	itemsByID := make(map[string]changeplanmodel.BusinessSystemChangeItem, len(items))
	for _, item := range items {
		itemsByID[item.ItemID] = item
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	result := make([]string, 0, len(items))
	var visit func(string) error
	visit = func(itemID string) error {
		if visited[itemID] {
			return nil
		}
		if visiting[itemID] {
			return conflict("backend.change_plan.package_dependency_cycle", "item_id", itemID)
		}
		visiting[itemID] = true
		item := itemsByID[itemID]
		dependencies := append([]changeplanmodel.BusinessChangeTarget(nil), item.Dependencies...)
		sort.Slice(dependencies, func(i, j int) bool {
			return packageResourceIdentity(dependencies[i].ResourceType, dependencies[i].ResourceKey) < packageResourceIdentity(dependencies[j].ResourceType, dependencies[j].ResourceKey)
		})
		for _, dependency := range dependencies {
			dependencyID := packageResourceIdentity(dependency.ResourceType, dependency.ResourceKey)
			if _, packaged := itemsByID[dependencyID]; packaged {
				if err := visit(dependencyID); err != nil {
					return err
				}
			}
		}
		visiting[itemID], visited[itemID] = false, true
		result = append(result, itemID)
		return nil
	}
	ids := make([]string, 0, len(items))
	for itemID := range itemsByID {
		ids = append(ids, itemID)
	}
	sort.Strings(ids)
	for _, itemID := range ids {
		if err := visit(itemID); err != nil {
			return nil, err
		}
	}
	return result, nil
}
