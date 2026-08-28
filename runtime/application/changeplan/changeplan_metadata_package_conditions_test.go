package changeplan

import (
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func validPackageForConditions(t *testing.T) (changeplanmodel.BusinessSystemPackage, changeplanmodel.Snapshot) {
	t.Helper()
	payload, resourceHash, err := canonicalPackagePayload(json.RawMessage(`{"key":"order"}`))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := changeplanmodel.Snapshot{RuntimeVersion: "runtime-v1", AuthoringContractVersion: "contract-v1", AuthoringContractHash: "contract-hash"}
	pkg := changeplanmodel.BusinessSystemPackage{
		PackageVersion: changeplanmodel.BusinessSystemPackageVersion,
		RuntimeVersion: snapshot.RuntimeVersion, AuthoringContractVersion: snapshot.AuthoringContractVersion, AuthoringContractHash: snapshot.AuthoringContractHash,
		Resources:               []changeplanmodel.BusinessSystemPackageResource{{ResourceType: "object", ResourceKey: "order", ResourceHash: resourceHash, Payload: payload}},
		EmptyWorkspaceApplyPlan: changeplanmodel.BusinessSystemPackageApplyPlan{Target: "empty_workspace", RequiresSnapshotBinding: true, RequiresReferenceBinding: true, Operations: []changeplanmodel.BusinessSystemPackageApplyOperation{{Operation: "create", ResourceType: "object", ResourceKey: "order", Payload: payload}}},
	}
	pkg.PackageHash = hashBusinessSystemPackage(pkg)
	return pkg, snapshot
}

func rehashPackageForConditions(pkg changeplanmodel.BusinessSystemPackage) changeplanmodel.BusinessSystemPackage {
	pkg.PackageHash = hashBusinessSystemPackage(pkg)
	return pkg
}

func TestValidateBusinessSystemPackageCoversEveryContractBoundary(t *testing.T) {
	valid, snapshot := validPackageForConditions(t)
	if err := validateBusinessSystemPackage(valid, snapshot); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		edit   func(*changeplanmodel.BusinessSystemPackage, *changeplanmodel.Snapshot)
		code   string
		rehash bool
	}{
		{name: "version", code: "backend.change_plan.package_version_unsupported", edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) { p.PackageVersion = "old" }},
		{name: "runtime contract", code: "backend.change_plan.package_contract_mismatch", edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.RuntimeVersion = "other"
		}},
		{name: "authoring version", code: "backend.change_plan.package_contract_mismatch", edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.AuthoringContractVersion = "other"
		}},
		{name: "authoring hash", code: "backend.change_plan.package_contract_mismatch", edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.AuthoringContractHash = "other"
		}},
		{name: "target", code: "backend.change_plan.package_apply_plan_invalid", edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.EmptyWorkspaceApplyPlan.Target = "existing_workspace"
		}},
		{name: "snapshot binding", code: "backend.change_plan.package_apply_plan_invalid", edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.EmptyWorkspaceApplyPlan.RequiresSnapshotBinding = false
		}},
		{name: "reference binding", code: "backend.change_plan.package_apply_plan_invalid", edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.EmptyWorkspaceApplyPlan.RequiresReferenceBinding = false
		}},
		{name: "package hash", code: "backend.change_plan.package_hash_mismatch", edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) { p.PackageHash = "wrong" }},
		{name: "blank resource type", code: "backend.change_plan.package_resource_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.Resources[0].ResourceType = " "
		}},
		{name: "blank resource key", code: "backend.change_plan.package_resource_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.Resources[0].ResourceKey = " "
		}},
		{name: "unsupported resource", code: "backend.change_plan.package_resource_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.Resources[0].ResourceType = "surface"
		}},
		{name: "duplicate resource", code: "backend.change_plan.package_resource_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.Resources = append(p.Resources, p.Resources[0])
		}},
		{name: "invalid resource payload", code: "backend.change_plan.package_resource_hash_mismatch", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.Resources[0].Payload = json.RawMessage(`{`)
		}},
		{name: "resource hash", code: "backend.change_plan.package_resource_hash_mismatch", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.Resources[0].ResourceHash = "wrong"
		}},
		{name: "existing target", code: "backend.change_plan.package_target_not_empty", edit: func(_ *changeplanmodel.BusinessSystemPackage, s *changeplanmodel.Snapshot) {
			s.ResourceSources = []changeplanmodel.ResourceSource{{ResourceType: "object", ResourceKey: "order"}}
		}},
		{name: "empty resources", code: "backend.change_plan.package_apply_plan_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.Resources = nil
			p.EmptyWorkspaceApplyPlan.Operations = nil
		}},
		{name: "operation count", code: "backend.change_plan.package_apply_plan_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.EmptyWorkspaceApplyPlan.Operations = nil
		}},
		{name: "operation kind", code: "backend.change_plan.package_apply_plan_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.EmptyWorkspaceApplyPlan.Operations[0].Operation = "update"
		}},
		{name: "operation resource missing", code: "backend.change_plan.package_apply_plan_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.EmptyWorkspaceApplyPlan.Operations[0].ResourceKey = "missing"
		}},
		{name: "duplicate operation", code: "backend.change_plan.package_apply_plan_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.Resources = append(p.Resources, changeplanmodel.BusinessSystemPackageResource{ResourceType: "field", ResourceKey: "order.status", ResourceHash: p.Resources[0].ResourceHash, Payload: p.Resources[0].Payload})
			p.EmptyWorkspaceApplyPlan.Operations = append(p.EmptyWorkspaceApplyPlan.Operations, p.EmptyWorkspaceApplyPlan.Operations[0])
		}},
		{name: "invalid operation payload", code: "backend.change_plan.package_apply_plan_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.EmptyWorkspaceApplyPlan.Operations[0].Payload = json.RawMessage(`{`)
		}},
		{name: "different operation payload", code: "backend.change_plan.package_apply_plan_invalid", rehash: true, edit: func(p *changeplanmodel.BusinessSystemPackage, _ *changeplanmodel.Snapshot) {
			p.EmptyWorkspaceApplyPlan.Operations[0].Payload = json.RawMessage(`{"key":"other"}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkg, snap := validPackageForConditions(t)
			tc.edit(&pkg, &snap)
			if tc.rehash {
				pkg = rehashPackageForConditions(pkg)
			}
			if code := apperror.CodeOf(validateBusinessSystemPackage(pkg, snap)); code != tc.code {
				t.Fatalf("code=%q want=%q", code, tc.code)
			}
		})
	}
}

func TestImportPackageDraftCoversAuthorizationIdentityDependencyCycleAndDefaultReason(t *testing.T) {
	pkg, snapshot := validPackageForConditions(t)
	repository := &changePlanDraftRepositoryFake{saveOK: true}
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, nil)
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph"}

	if _, err := service.ImportPackageDraft(t.Context(), "plan", "", 0, pkg, snapshot, graph, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("scope err=%v", err)
	}
	nonAdmin := changePlanAdmin()
	nonAdmin = changePlanWithoutPermissions(nonAdmin)
	if _, err := service.ImportPackageDraft(t.Context(), "plan", "", 0, pkg, snapshot, graph, nonAdmin); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission err=%v", err)
	}
	if _, err := service.ImportPackageDraft(t.Context(), " ", "", 0, pkg, snapshot, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.plan_id_required" {
		t.Fatalf("plan id err=%v", err)
	}

	draft, err := service.ImportPackageDraft(t.Context(), "plan", "", 0, pkg, snapshot, graph, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	var imported changeplanmodel.BusinessSystemChangePlan
	if err := json.Unmarshal(draft.Payload, &imported); err != nil || imported.BusinessReason == "" {
		t.Fatalf("imported=%#v err=%v", imported, err)
	}

	missing := pkg
	missing.Dependencies = []changeplanmodel.BusinessSystemPackageDependency{{FromResourceType: "object", FromResourceKey: "missing", ToResourceType: "object", ToResourceKey: "order"}}
	missing = rehashPackageForConditions(missing)
	if _, err := service.ImportPackageDraft(t.Context(), "missing", "reason", 0, missing, snapshot, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.package_dependency_invalid" {
		t.Fatalf("dependency err=%v", err)
	}

	cycle := pkg
	cycle.Resources = append(cycle.Resources, changeplanmodel.BusinessSystemPackageResource{ResourceType: "field", ResourceKey: "order.status", ResourceHash: cycle.Resources[0].ResourceHash, Payload: cycle.Resources[0].Payload})
	cycle.EmptyWorkspaceApplyPlan.Operations = append(cycle.EmptyWorkspaceApplyPlan.Operations, changeplanmodel.BusinessSystemPackageApplyOperation{Operation: "create", ResourceType: "field", ResourceKey: "order.status", Payload: cycle.Resources[0].Payload})
	cycle.Dependencies = []changeplanmodel.BusinessSystemPackageDependency{
		{FromResourceType: "object", FromResourceKey: "order", ToResourceType: "field", ToResourceKey: "order.status"},
		{FromResourceType: "field", FromResourceKey: "order.status", ToResourceType: "object", ToResourceKey: "order"},
	}
	cycle = rehashPackageForConditions(cycle)
	if _, err := service.ImportPackageDraft(t.Context(), "cycle", "reason", 0, cycle, snapshot, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.package_dependency_cycle" {
		t.Fatalf("cycle err=%v", err)
	}
}

func TestMetadataChangePlanMutationsRequiresRuntime(t *testing.T) {
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, nil, &changePlanAuditFake{}, nil)
	plan := changeplanmodel.BusinessSystemChangePlan{ReleaseOrder: []string{"object:order"}, Items: []changeplanmodel.BusinessSystemChangeItem{{ItemID: "object:order", Operation: "create", ResourceType: "object", ResourceKey: "order", After: json.RawMessage(`{"key":"order"}`)}}}
	if _, _, err := service.metadataChangePlanMutations(t.Context(), plan, changePlanAdmin()); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("runtime err=%v", err)
	}
}

func TestPackageReleaseOrderIgnoresDependenciesOutsidePackage(t *testing.T) {
	items := []changeplanmodel.BusinessSystemChangeItem{{
		ItemID: "action:order.submit",
		Dependencies: []changeplanmodel.BusinessChangeTarget{
			{ResourceType: "role", ResourceKey: "foundation_admin", Reason: "platform owned"},
			{ResourceType: "object", ResourceKey: "foundation_identity", Reason: "platform owned"},
		},
	}}
	order, err := packageReleaseOrder(items)
	if err != nil || len(order) != 1 || order[0] != "action:order.submit" {
		t.Fatalf("order=%v err=%v", order, err)
	}
}
