// Package identitysdkfixture builds SDK-native AccessBundle fixtures for
// Runtime tests.
// Production code must never import this package.
package identitysdkfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type Bundle struct {
	Key               string
	Permissions       []string
	FunctionGrants    []FunctionGrantFixture
	RecordScope       string
	DataPolicies      []DataPolicyFixture
	FieldPolicies     []FieldPolicyFixture
	ReferencePolicies []ReferencePolicyFixture
	ExportPolicies    []ExportPolicyFixture
	Guardrails        []GuardrailFixture
}

// FunctionGrantFixture carries the canonical resource/action decomposition for
// permission keys whose segments cannot be inferred from the opaque key.
type FunctionGrantFixture struct {
	PermissionKey string
	ResourceKey   string
	ActionKey     string
}

type DataPolicyFixture struct {
	ObjectKey   string
	Scope       string
	Read        bool
	Write       bool
	AuditDenial bool
	Predicate   *PredicateFixture
}

type FieldPolicyFixture struct {
	ObjectKey string
	FieldKey  string
	Read      bool
	Write     bool
	Export    bool
	Masked    bool
	Reason    string
	Policies  []FieldRuleFixture
}

type ReferencePolicyFixture struct {
	SourceObjectKey  string
	RelationFieldKey string
	TargetObjectKey  string
	DisplayFields    []string
	Mode             string
	Reason           string
}

type ExportPolicyFixture struct {
	ObjectKey string
	Mode      string
	Fields    []string
}

type GuardrailFixture struct {
	Key                  string
	DeniedPermissionKeys []string
	DataRestrictions     []DataRestrictionFixture
	FieldRestrictions    []FieldRestrictionFixture
}

type DataRestrictionFixture struct {
	ObjectKey string
	Actions   []string
	Reason    string
}

type FieldRestrictionFixture struct {
	ObjectKey string
	FieldKey  string
	Actions   []string
	Reason    string
}

type PredicateFixture struct {
	Operator    string
	Path        []RelationSegmentFixture
	FieldKey    string
	ValueSource string
	ClaimKey    string
	Values      []string
	Children    []PredicateFixture
}

type RelationSegmentFixture struct {
	Direction        string
	RelationFieldKey string
	TargetObjectKey  string
}

type FieldRuleFixture struct {
	Key          string
	Priority     int
	Actions      []string
	Effect       string
	Predicate    *PredicateFixture
	MaskStrategy *MaskFixture
	AuditDenial  bool
}

type MaskFixture struct {
	Type  string
	LastN int
}

type principalValue interface {
	principalmodel.Principal | *principalmodel.Principal
}

// Attach converts a declarative test fixture into the exact SDK AccessBundle
// consumed by production code. It cannot create a Plane-owned authorization
// fallback.
func Attach(principal principalmodel.Principal, spec Bundle) principalmodel.Principal {
	principal.RoleKey = strings.TrimSpace(spec.Key)
	principal.Permissions = append([]string(nil), spec.Permissions...)
	bundle := identitysdk.AccessBundle{
		ContractVersion:       identitysdk.CurrentPolicyBundleVersion,
		AuthorizationRevision: "test-authorization",
		ExpiresAt:             time.Now().Add(time.Hour),
		Subject: identitysdk.Subject{
			WorkspaceID:         identitysdk.WorkspaceID(valueOrDefault(principal.WorkspaceID, "test-workspace")),
			SubjectID:           identitysdk.SubjectID(valueOrDefault(principal.UserID, "test-subject")),
			WorkforceProfileID:  principal.WorkforceProfileID,
			DepartmentID:        principal.DepartmentID,
			DepartmentPath:      principal.DepartmentPath,
			ReportingPath:       principal.ReportingPath,
			ReportingSubjectIDs: subjectIDs(principal.ReportingUserIDs),
			OrganizationScopes: map[string][]string{
				"team_ids": principal.OrganizationScopes.TeamIDs, "store_ids": principal.OrganizationScopes.StoreIDs,
				"territory_ids": principal.OrganizationScopes.TerritoryIDs, "warehouse_ids": principal.OrganizationScopes.WarehouseIDs,
			},
		},
	}
	functionGrantKeys := map[string]bool{}
	grants := []identitysdk.FunctionGrant{}
	addGrant := func(resource identitysdk.ResourceType, action identitysdk.Action) {
		grantKey := string(resource) + "\x00" + string(action)
		if functionGrantKeys[grantKey] {
			return
		}
		grant := identitysdk.FunctionGrant{Resource: resource, Action: action, Effect: identitysdk.EffectAllow}
		bundle.FunctionGrants = append(bundle.FunctionGrants, grant)
		grants = append(grants, grant)
		functionGrantKeys[grantKey] = true
	}
	explicitFunctionGrants := map[string]FunctionGrantFixture{}
	for _, grant := range spec.FunctionGrants {
		explicitFunctionGrants[strings.TrimSpace(grant.PermissionKey)] = grant
	}
	for _, permission := range spec.Permissions {
		permission = strings.TrimSpace(permission)
		if grant, exists := explicitFunctionGrants[permission]; exists {
			addGrant(identitysdk.ResourceType(strings.TrimSpace(grant.ResourceKey)), identitysdk.Action(strings.TrimSpace(grant.ActionKey)))
			continue
		}
		resource, action, ok := permissionParts(permission)
		if !ok {
			continue
		}
		addGrant(resource, action)
	}
	for permissionKey, grant := range explicitFunctionGrants {
		if containsFixturePermission(spec.Permissions, permissionKey) {
			continue
		}
		addGrant(identitysdk.ResourceType(strings.TrimSpace(grant.ResourceKey)), identitysdk.Action(strings.TrimSpace(grant.ActionKey)))
	}
	explicitDataResources := map[identitysdk.ResourceType]bool{}
	for _, permission := range spec.DataPolicies {
		explicitDataResources[identitysdk.ResourceType(strings.TrimSpace(permission.ObjectKey))] = true
	}
	defaultScope := strings.TrimSpace(spec.RecordScope)
	if defaultScope == "" && len(spec.DataPolicies) == 0 {
		// Test callers that exercise a record path with only a function grant
		// mean an unrestricted SDK data policy. Production Identity always
		// materializes this policy from its own spec configuration and catalog.
		defaultScope = "all_records"
	}
	if defaultScope != "" && defaultScope != "none" {
		dataPolicyKeys := map[string]bool{}
		for _, grant := range grants {
			if explicitDataResources[grant.Resource] {
				continue
			}
			dataAction := sdkDataAction(grant.Action)
			policyKey := string(grant.Resource) + "\x00" + string(dataAction)
			if dataPolicyKeys[policyKey] {
				continue
			}
			dataPolicyKeys[policyKey] = true
			policy := dataPolicy(grant.Resource, dataAction, defaultScope, false, nil)
			policy.Key += ".record_scope"
			bundle.DataPolicies = append(bundle.DataPolicies, policy)
		}
	}
	for permissionIndex, permission := range spec.DataPolicies {
		actions := dataPolicyActions(permission)
		for actionIndex, action := range actions {
			policy := dataPolicy(identitysdk.ResourceType(permission.ObjectKey), action, permission.Scope, permission.AuditDenial, permission.Predicate)
			policy.Key += fmt.Sprintf(".%d.%d", permissionIndex, actionIndex)
			bundle.DataPolicies = append(bundle.DataPolicies, policy)
		}
	}
	fieldEnvelopes := map[identitysdk.ResourceType]*identitysdk.FieldPolicy{}
	for _, grant := range grants {
		envelope := fieldEnvelopes[grant.Resource]
		if envelope == nil {
			envelope = &identitysdk.FieldPolicy{Resource: grant.Resource, Field: "*"}
			fieldEnvelopes[grant.Resource] = envelope
		}
		switch grant.Action {
		case "read", "list", "view", "search", "report", "audit":
			envelope.Read = true
		case "export":
			envelope.Export = true
		case "*":
			envelope.Read, envelope.Write, envelope.Export = true, true, true
		default:
			envelope.Write = true
		}
	}
	explicitFieldKeys := map[string]bool{}
	for _, permission := range spec.FieldPolicies {
		explicitFieldKeys[strings.TrimSpace(permission.ObjectKey)+"\x00"+strings.TrimSpace(permission.FieldKey)] = true
	}
	fieldEnvelopeResources := make([]string, 0, len(fieldEnvelopes))
	for resource := range fieldEnvelopes {
		fieldEnvelopeResources = append(fieldEnvelopeResources, string(resource))
	}
	sort.Strings(fieldEnvelopeResources)
	for _, resourceKey := range fieldEnvelopeResources {
		resource := identitysdk.ResourceType(resourceKey)
		envelope := fieldEnvelopes[resource]
		if !explicitFieldKeys[string(resource)+"\x00*"] {
			bundle.FieldPolicies = append(bundle.FieldPolicies, *envelope)
		}
	}
	for _, permission := range spec.FieldPolicies {
		bundle.FieldPolicies = append(bundle.FieldPolicies, identitysdk.FieldPolicy{
			Resource: identitysdk.ResourceType(permission.ObjectKey), Field: permission.FieldKey,
			Read: permission.Read, Write: permission.Write, Export: permission.Export, Masked: permission.Masked,
			Reason: permission.Reason, Rules: sdkFieldRules(permission.Policies),
		})
	}
	for _, permission := range spec.ReferencePolicies {
		bundle.ReferencePolicies = append(bundle.ReferencePolicies, identitysdk.ReferencePolicy{
			SourceResource: identitysdk.ResourceType(strings.TrimSpace(permission.SourceObjectKey)), Reference: strings.TrimSpace(permission.RelationFieldKey),
			TargetResource: identitysdk.ResourceType(strings.TrimSpace(permission.TargetObjectKey)), DisplayFields: append([]string(nil), permission.DisplayFields...),
			Allowed: permission.Mode != "deny", Reason: permission.Reason,
		})
	}
	for _, rule := range spec.ExportPolicies {
		if strings.TrimSpace(rule.Mode) == "all_fields" {
			continue
		}
		mode := identitysdk.ExportModeDeny
		if rule.Mode != "deny" {
			mode = identitysdk.ExportModeAllowList
		}
		bundle.ExportPolicies = append(bundle.ExportPolicies, identitysdk.ExportPolicy{Resource: identitysdk.ResourceType(rule.ObjectKey), Mode: mode, Fields: append([]string(nil), rule.Fields...)})
	}
	for _, guardrail := range spec.Guardrails {
		for _, permission := range guardrail.DeniedPermissionKeys {
			resource, action, ok := permissionParts(permission)
			if ok {
				bundle.Guardrails = append(bundle.Guardrails, identitysdk.Guardrail{Key: guardrail.Key + ":" + permission, Resource: resource, Action: action, Effect: identitysdk.EffectDeny})
			}
		}
		for _, restriction := range guardrail.DataRestrictions {
			for _, action := range restriction.Actions {
				bundle.Guardrails = append(bundle.Guardrails, identitysdk.Guardrail{Key: guardrail.Key + ":data:" + restriction.ObjectKey + ":" + action, Resource: identitysdk.ResourceType(restriction.ObjectKey), Action: identitysdk.Action(action), Effect: identitysdk.EffectDeny, Reason: restriction.Reason})
			}
		}
		for _, restriction := range guardrail.FieldRestrictions {
			for _, action := range restriction.Actions {
				bundle.Guardrails = append(bundle.Guardrails, identitysdk.Guardrail{Key: guardrail.Key + ":field:" + restriction.ObjectKey + ":" + restriction.FieldKey + ":" + action, Resource: identitysdk.ResourceType(restriction.ObjectKey), Action: identitysdk.Action(action), Field: restriction.FieldKey, Effect: identitysdk.EffectDeny, Reason: restriction.Reason})
			}
		}
	}
	if revision := strings.TrimSpace(principal.AuthorizationRevision); revision == "" || strings.HasPrefix(revision, "test-authorization:") {
		revisionBundle := bundle
		revisionBundle.AuthorizationRevision = ""
		revisionBundle.ExpiresAt = time.Time{}
		encoded, _ := json.Marshal(revisionBundle)
		digest := sha256.Sum256(encoded)
		bundle.AuthorizationRevision = identitysdk.AuthorizationRevision("test-authorization:" + hex.EncodeToString(digest[:]))
	} else {
		bundle.AuthorizationRevision = identitysdk.AuthorizationRevision(revision)
	}
	principal.AccessBundle = &bundle
	principal.AuthorizationRevision = string(bundle.AuthorizationRevision)
	return principal
}

func AttachPointer(principal principalmodel.Principal, spec Bundle) *principalmodel.Principal {
	resolved := Attach(principal, spec)
	return &resolved
}

// FromPrincipal projects an SDK AccessBundle back into the declarative
// fixture shape used by tests. It must not be used by production code: the
// AccessBundle remains the only authorization state exercised by the test.
func FromPrincipal(principal principalmodel.Principal) Bundle {
	spec := Bundle{Key: strings.TrimSpace(principal.RoleKey), Permissions: principal.PermissionKeys()}
	if principal.AccessBundle == nil {
		return spec
	}
	if len(principal.AccessBundle.DataPolicies) > 0 {
		// Preserve an explicit removal of projected data policies during fixture
		// mutation instead of re-inventing a default unrestricted scope.
		spec.RecordScope = "none"
	}
	dataByResource := map[identitysdk.ResourceType]int{}
	for _, policy := range principal.AccessBundle.DataPolicies {
		index, exists := dataByResource[policy.Resource]
		if !exists {
			spec.DataPolicies = append(spec.DataPolicies, DataPolicyFixture{
				ObjectKey: string(policy.Resource), Scope: scopeFromPredicate(policy.Predicate),
			})
			index = len(spec.DataPolicies) - 1
			dataByResource[policy.Resource] = index
		}
		permission := &spec.DataPolicies[index]
		permission.AuditDenial = permission.AuditDenial || policy.AuditDenial
		if permission.Predicate == nil && scopeFromPredicate(policy.Predicate) == "custom" {
			predicate := policyExpressionFromSDK(policy.Predicate)
			permission.Predicate = &predicate
		}
		switch policy.Action {
		case "read":
			permission.Read = policy.Effect == identitysdk.EffectAllow
		case "write", "create", "update", "delete":
			permission.Write = permission.Write || policy.Effect == identitysdk.EffectAllow
		}
	}
	for _, policy := range principal.AccessBundle.FieldPolicies {
		spec.FieldPolicies = append(spec.FieldPolicies, FieldPolicyFixture{
			ObjectKey: string(policy.Resource), FieldKey: policy.Field,
			Read: policy.Read, Write: policy.Write, Export: policy.Export, Masked: policy.Masked,
			Reason: policy.Reason, Policies: fieldRulesFromSDK(policy.Rules),
		})
	}
	for _, policy := range principal.AccessBundle.ReferencePolicies {
		mode := "allow"
		if !policy.Allowed {
			mode = "deny"
		}
		spec.ReferencePolicies = append(spec.ReferencePolicies, ReferencePolicyFixture{
			SourceObjectKey: string(policy.SourceResource), RelationFieldKey: policy.Reference,
			TargetObjectKey: string(policy.TargetResource), DisplayFields: append([]string(nil), policy.DisplayFields...), Mode: mode, Reason: policy.Reason,
		})
	}
	for _, policy := range principal.AccessBundle.ExportPolicies {
		mode := "deny"
		if policy.Mode == identitysdk.ExportModeAllowList {
			mode = "fields"
		}
		spec.ExportPolicies = append(spec.ExportPolicies, ExportPolicyFixture{ObjectKey: string(policy.Resource), Mode: mode, Fields: append([]string(nil), policy.Fields...)})
	}
	return spec
}

// Mutate rebuilds the SDK AccessBundle after changing a test fixture.
func Mutate(principal *principalmodel.Principal, mutate func(*Bundle)) {
	if principal == nil {
		return
	}
	spec := FromPrincipal(*principal)
	if mutate != nil {
		mutate(&spec)
	}
	*principal = Attach(*principal, spec)
}

func Set(principal *principalmodel.Principal, spec Bundle) {
	if principal == nil {
		return
	}
	*principal = Attach(*principal, spec)
}

// Of and WithMutation accept either a Principal value or pointer.
func Of(value any) Bundle {
	switch principal := value.(type) {
	case principalmodel.Principal:
		return FromPrincipal(principal)
	case *principalmodel.Principal:
		if principal != nil {
			return FromPrincipal(*principal)
		}
	}
	return Bundle{}
}

func With[T principalValue](principal T, spec Bundle) T {
	switch value := any(principal).(type) {
	case principalmodel.Principal:
		return any(Attach(value, spec)).(T)
	case *principalmodel.Principal:
		if value == nil {
			return principal
		}
		resolved := Attach(*value, spec)
		return any(&resolved).(T)
	default:
		return principal
	}
}

func WithMutation[T principalValue](principal T, mutate func(*Bundle)) T {
	spec := Of(principal)
	if mutate != nil {
		mutate(&spec)
	}
	return With(principal, spec)
}

func permissionParts(permission string) (identitysdk.ResourceType, identitysdk.Action, bool) {
	permission = strings.TrimSpace(permission)
	separator := strings.LastIndex(permission, ".")
	if separator <= 0 || separator == len(permission)-1 {
		return "", "", false
	}
	return identitysdk.ResourceType(permission[:separator]), identitysdk.Action(permission[separator+1:]), true
}

func containsFixturePermission(permissions []string, permissionKey string) bool {
	permissionKey = strings.TrimSpace(permissionKey)
	for _, permission := range permissions {
		if strings.TrimSpace(permission) == permissionKey {
			return true
		}
	}
	return false
}

func dataPolicyActions(permission DataPolicyFixture) []identitysdk.DataAction {
	actions := make([]identitysdk.DataAction, 0, 2)
	if permission.Read {
		actions = append(actions, identitysdk.DataActionRead)
	}
	if permission.Write {
		actions = append(actions, identitysdk.DataActionWrite)
	}
	return actions
}

func sdkDataAction(action identitysdk.Action) identitysdk.DataAction {
	switch action {
	case "read", "list", "view", "search", "report", "audit", "export":
		return identitysdk.DataActionRead
	default:
		return identitysdk.DataActionWrite
	}
}

func dataPolicy(resource identitysdk.ResourceType, action identitysdk.DataAction, scope string, auditDenial bool, predicate *PredicateFixture) identitysdk.DataPolicy {
	resolved := scopePredicate(scope)
	if predicate != nil {
		resolved = sdkPolicyExpression(*predicate)
	}
	return identitysdk.DataPolicy{
		Key: string(resource) + "." + string(action) + ".test", Resource: resource, Action: action, Effect: identitysdk.EffectAllow,
		Predicate: resolved, AuditDenial: auditDenial,
	}
}

func sdkPolicyExpression(value PredicateFixture) identitysdk.Predicate {
	switch strings.TrimSpace(value.Operator) {
	case "and":
		result := identitysdk.Predicate{All: make([]identitysdk.Predicate, 0, len(value.Children))}
		for _, child := range value.Children {
			result.All = append(result.All, sdkPolicyExpression(child))
		}
		return result
	case "or":
		result := identitysdk.Predicate{Any: make([]identitysdk.Predicate, 0, len(value.Children))}
		for _, child := range value.Children {
			result.Any = append(result.Any, sdkPolicyExpression(child))
		}
		return result
	case "not":
		if len(value.Children) == 1 {
			child := sdkPolicyExpression(value.Children[0])
			return identitysdk.Predicate{Not: &child}
		}
	}
	operator := identitysdk.Operator(strings.TrimSpace(value.Operator))
	if operator == "" {
		operator = identitysdk.OperatorEqual
	}
	var expected any = append([]string(nil), value.Values...)
	if operator == identitysdk.OperatorEqual && len(value.Values) == 1 {
		expected = value.Values[0]
	}
	if value.ValueSource == "actor_claim" {
		expected = sdkFixtureClaim(value.ClaimKey)
	}
	path := make([]identitysdk.RelationSegment, 0, len(value.Path))
	for _, segment := range value.Path {
		path = append(path, identitysdk.RelationSegment{Direction: identitysdk.RelationDirection(segment.Direction), Reference: segment.RelationFieldKey, TargetResource: identitysdk.ResourceType(segment.TargetObjectKey)})
	}
	return identitysdk.Predicate{Fact: value.FieldKey, Path: path, Operator: operator, Value: expected}
}

func sdkFixtureClaim(key string) string {
	switch strings.TrimSpace(key) {
	case "user_id":
		return "$subject.id"
	case "department_id":
		return "$subject.department_id"
	case "workforce_profile_id":
		return "$subject.workforce_profile_id"
	case "reporting_user_ids", "reporting_subject_ids":
		return "$subject.reporting_subject_ids"
	case "team_ids", "store_ids", "territory_ids", "warehouse_ids":
		return "$subject.organization_scopes." + strings.TrimSpace(key)
	case "business_profile_id":
		return "$context.business_profile_id"
	default:
		return "$context.claims." + strings.TrimSpace(key)
	}
}

func sdkFieldRules(values []FieldRuleFixture) []identitysdk.FieldRule {
	result := make([]identitysdk.FieldRule, 0, len(values))
	for _, value := range values {
		actions := make([]identitysdk.Action, 0, len(value.Actions))
		for _, action := range value.Actions {
			actions = append(actions, identitysdk.Action(action))
		}
		var predicate *identitysdk.Predicate
		if value.Predicate != nil {
			converted := sdkPolicyExpression(*value.Predicate)
			predicate = &converted
		}
		var strategy *identitysdk.MaskStrategy
		if value.MaskStrategy != nil {
			strategy = &identitysdk.MaskStrategy{Type: identitysdk.MaskType(value.MaskStrategy.Type), LastN: value.MaskStrategy.LastN}
		}
		result = append(result, identitysdk.FieldRule{Key: value.Key, Priority: value.Priority, Actions: actions, Effect: identitysdk.FieldEffect(value.Effect), Predicate: predicate, MaskStrategy: strategy, AuditDenial: value.AuditDenial})
	}
	return result
}

func policyExpressionFromSDK(value identitysdk.Predicate) PredicateFixture {
	if len(value.All) > 0 {
		result := PredicateFixture{Operator: "and"}
		for _, child := range value.All {
			result.Children = append(result.Children, policyExpressionFromSDK(child))
		}
		return result
	}
	if len(value.Any) > 0 {
		result := PredicateFixture{Operator: "or"}
		for _, child := range value.Any {
			result.Children = append(result.Children, policyExpressionFromSDK(child))
		}
		return result
	}
	if value.Not != nil {
		return PredicateFixture{Operator: "not", Children: []PredicateFixture{policyExpressionFromSDK(*value.Not)}}
	}
	result := PredicateFixture{Operator: string(value.Operator), FieldKey: value.Fact, ValueSource: "literal"}
	for _, segment := range value.Path {
		result.Path = append(result.Path, RelationSegmentFixture{Direction: string(segment.Direction), RelationFieldKey: segment.Reference, TargetObjectKey: string(segment.TargetResource)})
	}
	switch expected := value.Value.(type) {
	case []string:
		result.Values = append([]string(nil), expected...)
	case string:
		if strings.HasPrefix(expected, "$subject.") || strings.HasPrefix(expected, "$context.") {
			result.ValueSource = "actor_claim"
			result.ClaimKey = fixtureClaimFromReference(expected)
		} else {
			result.Values = []string{expected}
		}
	default:
		result.Values = []string{fmt.Sprint(expected)}
	}
	return result
}

func fixtureClaimFromReference(value string) string {
	switch value {
	case "$subject.id":
		return "user_id"
	case "$subject.department_id":
		return "department_id"
	case "$subject.workforce_profile_id":
		return "workforce_profile_id"
	case "$subject.reporting_subject_ids":
		return "reporting_user_ids"
	case "$context.business_profile_id":
		return "business_profile_id"
	}
	value = strings.TrimPrefix(value, "$subject.organization_scopes.")
	value = strings.TrimPrefix(value, "$context.claims.")
	return value
}

func fieldRulesFromSDK(values []identitysdk.FieldRule) []FieldRuleFixture {
	result := make([]FieldRuleFixture, 0, len(values))
	for _, value := range values {
		actions := make([]string, len(value.Actions))
		for index := range value.Actions {
			actions[index] = string(value.Actions[index])
		}
		var predicate *PredicateFixture
		if value.Predicate != nil {
			converted := policyExpressionFromSDK(*value.Predicate)
			predicate = &converted
		}
		var strategy *MaskFixture
		if value.MaskStrategy != nil {
			strategy = &MaskFixture{Type: string(value.MaskStrategy.Type), LastN: value.MaskStrategy.LastN}
		}
		result = append(result, FieldRuleFixture{Key: value.Key, Priority: value.Priority, Actions: actions, Effect: string(value.Effect), Predicate: predicate, MaskStrategy: strategy, AuditDenial: value.AuditDenial})
	}
	return result
}

func scopePredicate(scope string) identitysdk.Predicate {
	switch strings.TrimSpace(scope) {
	case "owned_records", "owned":
		return identitysdk.Predicate{Fact: "owner_id", Operator: identitysdk.OperatorEqual, Value: "$subject.id"}
	case "department":
		return identitysdk.Predicate{Fact: "department_id", Operator: identitysdk.OperatorEqual, Value: "$subject.department_id"}
	case "department_and_children":
		return identitysdk.Predicate{Fact: "department_path", Operator: identitysdk.OperatorPrefix, Value: "$subject.department_path"}
	case "subordinates":
		return identitysdk.Predicate{Fact: "owner_id", Operator: identitysdk.OperatorIn, Value: "$subject.reporting_subject_ids"}
	default:
		return identitysdk.Predicate{Fact: "id", Operator: identitysdk.OperatorExists, Value: true}
	}
}

func scopeFromPredicate(predicate identitysdk.Predicate) string {
	switch {
	case predicate.Fact == "owner_id" && predicate.Operator == identitysdk.OperatorEqual:
		return "owned_records"
	case predicate.Fact == "department_id" && predicate.Operator == identitysdk.OperatorEqual:
		return "department"
	case predicate.Fact == "department_path" && predicate.Operator == identitysdk.OperatorPrefix:
		return "department_and_children"
	case predicate.Fact == "owner_id" && predicate.Operator == identitysdk.OperatorIn:
		return "subordinates"
	case predicate.Fact == "id" && predicate.Operator == identitysdk.OperatorExists:
		return "all_records"
	default:
		return "custom"
	}
}

func subjectIDs(values []string) []identitysdk.SubjectID {
	out := make([]identitysdk.SubjectID, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, identitysdk.SubjectID(value))
		}
	}
	return out
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
