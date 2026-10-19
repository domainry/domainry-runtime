package service

import (
	"fmt"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func recordAuthorizationParentReadScope(object definitionmodel.ObjectSchema, objects []definitionmodel.ObjectSchema, principal principalmodel.Principal) (*recordmodel.RecordScopeExpression, error) {
	binding := object.AuthorizationParent
	var parent definitionmodel.ObjectSchema
	for _, candidate := range objects {
		if candidate.Key == binding.ObjectKey {
			parent = candidate
		}
	}
	field, found := sdkScopeObjectField(object, binding.RelationField)
	if parent.Key == "" || parent.Key == object.Key || parent.AuthorizationParent != nil || !found || field.Type != "relation" || !field.Required || sdkScopeRelationTarget(field) != parent.Key {
		return nil, fmt.Errorf("invalid authorization parent for %s", object.Key)
	}
	// One explicit parent, not a recursive walk through arbitrary relations.
	// Compile that parent's live read scope once; no child ACL is manufactured.
	scope, err, _ := RecordCompileSDKDataScopeExpression(parent, objects, principal, "read")
	if err != nil {
		return nil, err
	}
	path := recordmodel.RecordScopePathSegment{SourceObjectKey: object.Key, Direction: "forward", RelationFieldKey: binding.RelationField, TargetObjectKey: parent.Key}
	exists := recordmodel.RecordScopeExpression{Operator: "exists", FieldKey: "id", Path: []recordmodel.RecordScopePathSegment{path}}
	if scope == nil {
		// Even unrestricted parents must exist in the same Workspace. An
		// orphaned or cross-Workspace child is never a readable record.
		return &exists, nil
	}
	return &recordmodel.RecordScopeExpression{Operator: "and", Children: []recordmodel.RecordScopeExpression{exists, recordParentScopePath(*scope, path)}}, nil
}

func recordParentScopePath(scope recordmodel.RecordScopeExpression, path recordmodel.RecordScopePathSegment) recordmodel.RecordScopeExpression {
	if len(scope.Children) > 0 {
		scope.Children = append([]recordmodel.RecordScopeExpression(nil), scope.Children...)
		for index := range scope.Children {
			scope.Children[index] = recordParentScopePath(scope.Children[index], path)
		}
	} else {
		scope.Path = append([]recordmodel.RecordScopePathSegment{path}, scope.Path...)
	}
	return scope
}
