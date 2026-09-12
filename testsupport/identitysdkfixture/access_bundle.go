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
	Action      string
	Scope       identitysdk.DataScope
	Read        bool
	Write       bool
	AuditDenial bool
	Predicate   *PredicateFixture
}

// DataPoliciesForPermissions creates one explicit SDK data policy for every
// exact permission key. It exists only to keep test setup compact; unlike the
// removed role-wide default it cannot silently broaden an unrelated Action.
func DataPoliciesForPermissions(permissionKeys []string, scope identitysdk.DataScope) []DataPolicyFixture {
	if !scope.Valid() {
		panic("identity SDK fixture requires a canonical data scope")
	}
	result := make([]DataPolicyFixture, 0, len(permissionKeys))
	for _, permissionKey := range permissionKeys {
		resource, action, ok := permissionParts(permissionKey)
		if !ok {
			continue
		}
		result = append(result, DataPolicyFixture{
			ObjectKey: string(resource),
			Action:    string(action),
			Scope:     scope,
			Read:      fixtureReadAction(action),
			Write:     !fixtureReadAction(action),
		})
	}
	return result
}

type FieldPolicyFixture struct {
	ObjectKey   string
	FieldKey    string
	Read        bool
	Write       bool
	Export      bool
	Masked      bool
	Reason      string
	AuditDenial bool
	Policies    []FieldRuleFixture
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
// consumed by production code. A compact Permissions list represents complete
// all-scope test grants unless DataPolicies explicitly supplies narrower or
// denial-only policy. This mirrors compiler expansion without restoring a
// production authorization fallback or duplicating permission keys at call
// sites.
func Attach(principal principalmodel.Principal, spec Bundle) principalmodel.Principal {
	principal.RoleKey = strings.TrimSpace(spec.Key)
	principal.Permissions = append([]string(nil), spec.Permissions...)
	bundle := identitysdk.AccessBundle{
		ContractVersion:       identitysdk.CurrentPolicyBundleVersion,
		AuthorizationRevision: "test-authorization",
		ExpiresAt:             time.Now().Add(time.Hour),
		Subject: identitysdk.Subject{
			WorkspaceID:           identitysdk.WorkspaceID(valueOrDefault(principal.WorkspaceID, "test-workspace")),
			SubjectID:             identitysdk.SubjectID(valueOrDefault(principal.UserID, "test-subject")),
			OrgID:                 principal.OrgID,
			OrgScopeIDs:           append([]string(nil), principal.OrgScopeIDs...),
			SupportOrgID:          principal.SupportOrgID,
			SupportOrgScopeIDs:    append([]string(nil), principal.SupportOrgScopeIDs...),
			ReportingScopeUserIDs: subjectIDs(principal.ReportingScopeUserIDs),
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
	for index, grant := range grants {
		if dataPolicyDeclaredForGrant(spec.DataPolicies, grant) {
			continue
		}
		policy := dataPolicy(grant.Resource, grant.Action, identitysdk.DataScopeAll, false, nil)
		policy.Key += fmt.Sprintf(".default.%d", index)
		bundle.DataPolicies = append(bundle.DataPolicies, policy)
	}
	for permissionIndex, permission := range spec.DataPolicies {
		actions := dataPolicyActions(permission, grants)
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
			Reason: permission.Reason, AuditDenial: permission.AuditDenial, Rules: sdkFieldRules(permission.Policies),
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

func subjectIDs(values []string) []identitysdk.SubjectID {
	result := make([]identitysdk.SubjectID, len(values))
	for index, value := range values {
		result[index] = identitysdk.SubjectID(value)
	}
	return result
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
	dataByResource := map[identitysdk.ResourceType]int{}
	for _, policy := range principal.AccessBundle.DataPolicies {
		scope := dataPolicyScope(policy)
		index, exists := dataByResource[policy.Resource]
		if !exists {
			spec.DataPolicies = append(spec.DataPolicies, DataPolicyFixture{
				ObjectKey: string(policy.Resource), Scope: scope,
			})
			index = len(spec.DataPolicies) - 1
			dataByResource[policy.Resource] = index
		}
		permission := &spec.DataPolicies[index]
		permission.AuditDenial = permission.AuditDenial || policy.AuditDenial
		if permission.Predicate == nil && !scope.Valid() && !policy.Predicate.IsZero() {
			predicate := policyExpressionFromSDK(policy.Predicate)
			permission.Predicate = &predicate
		}
		if fixtureReadAction(policy.Action) {
			permission.Read = policy.Effect == identitysdk.EffectAllow
		} else {
			permission.Write = permission.Write || policy.Effect == identitysdk.EffectAllow
		}
	}
	for _, policy := range principal.AccessBundle.FieldPolicies {
		spec.FieldPolicies = append(spec.FieldPolicies, FieldPolicyFixture{
			ObjectKey: string(policy.Resource), FieldKey: policy.Field,
			Read: policy.Read, Write: policy.Write, Export: policy.Export, Masked: policy.Masked,
			Reason: policy.Reason, AuditDenial: policy.AuditDenial, Policies: fieldRulesFromSDK(policy.Rules),
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

func dataPolicyActions(permission DataPolicyFixture, grants []identitysdk.FunctionGrant) []identitysdk.Action {
	actions := []identitysdk.Action{}
	resource := identitysdk.ResourceType(strings.TrimSpace(permission.ObjectKey))
	if action := identitysdk.Action(strings.TrimSpace(permission.Action)); action != "" {
		for _, grant := range grants {
			if grant.Resource == resource && grant.Action == action {
				return []identitysdk.Action{action}
			}
		}
		return actions
	}
	for _, grant := range grants {
		if grant.Resource != resource || fixtureReadAction(grant.Action) && !permission.Read || !fixtureReadAction(grant.Action) && !permission.Write {
			continue
		}
		if !containsFixtureAction(actions, grant.Action) {
			actions = append(actions, grant.Action)
		}
	}
	return actions
}

func dataPolicyDeclaredForGrant(policies []DataPolicyFixture, grant identitysdk.FunctionGrant) bool {
	for _, policy := range policies {
		if strings.TrimSpace(policy.ObjectKey) != strings.TrimSpace(string(grant.Resource)) {
			continue
		}
		action := strings.TrimSpace(policy.Action)
		if action == "" || action == strings.TrimSpace(string(grant.Action)) {
			return true
		}
	}
	return false
}

func fixtureReadAction(action identitysdk.Action) bool {
	switch action {
	case "read", "list", "view", "search", "report", "audit", "export":
		return true
	default:
		return false
	}
}

func containsFixtureAction(actions []identitysdk.Action, expected identitysdk.Action) bool {
	for _, action := range actions {
		if action == expected {
			return true
		}
	}
	return false
}

func dataPolicy(resource identitysdk.ResourceType, action identitysdk.Action, scope identitysdk.DataScope, auditDenial bool, predicate *PredicateFixture) identitysdk.DataPolicy {
	resolved := scopePredicate(scope)
	if predicate != nil {
		resolved = sdkPolicyExpression(*predicate)
	}
	var dataScopes []identitysdk.DataScope
	if scope.Valid() {
		dataScopes = []identitysdk.DataScope{scope}
	}
	return identitysdk.DataPolicy{
		Key: string(resource) + "." + string(action) + ".test", Resource: resource, Action: action, Effect: identitysdk.EffectAllow,
		DataScopes: dataScopes, Predicate: resolved, AuditDenial: auditDenial,
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
	case "org_id":
		return "$subject.org_id"
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
	case "$subject.org_id":
		return "org_id"
	case "$context.business_profile_id":
		return "business_profile_id"
	}
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

func scopePredicate(scope identitysdk.DataScope) identitysdk.Predicate {
	switch scope {
	case "owner":
		return identitysdk.Predicate{Fact: "owner_user_id", Operator: identitysdk.OperatorEqual, Value: "$subject.id"}
	case "org":
		return identitysdk.Predicate{Fact: "owner_org_id", Operator: identitysdk.OperatorEqual, Value: "$subject.org_id"}
	case "org_child":
		return identitysdk.Predicate{Fact: "owner_org_id", Operator: identitysdk.OperatorIn, Value: "$subject.org_scope_ids"}
	case "target_org":
		return identitysdk.Predicate{Fact: "owner_org_id", Operator: identitysdk.OperatorIn, Value: "$subject.support_org_scope_ids"}
	case "all":
		return identitysdk.Predicate{}
	default:
		return identitysdk.Predicate{Fact: "id", Operator: identitysdk.OperatorIn, Value: []string{}}
	}
}

func dataPolicyScope(policy identitysdk.DataPolicy) identitysdk.DataScope {
	if len(policy.DataScopes) == 1 && policy.DataScopes[0].Valid() {
		return policy.DataScopes[0]
	}
	return scopeFromPredicate(policy.Predicate)
}

func scopeFromPredicate(predicate identitysdk.Predicate) identitysdk.DataScope {
	switch {
	case predicate.Fact == "owner_user_id" && predicate.Operator == identitysdk.OperatorEqual:
		return "owner"
	case predicate.Fact == "owner_org_id" && predicate.Operator == identitysdk.OperatorEqual:
		return "org"
	case predicate.Fact == "owner_org_id" && predicate.Operator == identitysdk.OperatorIn && predicate.Value == "$subject.support_org_scope_ids":
		return "target_org"
	case predicate.Fact == "owner_org_id" && predicate.Operator == identitysdk.OperatorIn:
		return "org_child"
	case predicate.Fact == "id" && predicate.Operator == identitysdk.OperatorExists:
		return "all"
	default:
		return ""
	}
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
