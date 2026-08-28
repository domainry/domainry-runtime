package validation

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	"fmt"
	"reflect"
	"sort"
	"strings"
)

type ReviewOptions struct {
	ApproveDestructive bool
}

type ReviewChange struct {
	Kind        string `json:"kind"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Destructive bool   `json:"destructive"`
	Approved    bool   `json:"approved"`
}

type ReviewResult struct {
	Changes  []ReviewChange `json:"changes"`
	Blockers []ReviewChange `json:"blockers"`
}

func (result ReviewResult) HasBlockers() bool {
	return len(result.Blockers) > 0
}

func ReviewManifestUpdate(previous manifestmodel.ManifestSchema, next manifestmodel.ManifestSchema, opts ReviewOptions) ReviewResult {
	review := manifestReview{
		previous: previous,
		next:     next,
		opts:     opts,
	}
	review.reviewObjects()
	review.reviewIdentityProfileExtensions()
	sort.Slice(review.result.Changes, func(i, j int) bool {
		return review.result.Changes[i].Path < review.result.Changes[j].Path
	})
	sort.Slice(review.result.Blockers, func(i, j int) bool {
		return review.result.Blockers[i].Path < review.result.Blockers[j].Path
	})
	return review.result
}

func (review *manifestReview) reviewIdentityProfileExtensions() {
	previous := identityProfileExtensionMap(review.previous.IdentityProfileExtensions)
	next := identityProfileExtensionMap(review.next.IdentityProfileExtensions)
	for objectKey, previousExtension := range previous {
		nextExtension, exists := next[objectKey]
		path := "identity_profile_extensions." + objectKey
		if !exists {
			review.add("remove_identity_profile_extension", path, "removing a Profile Extension from unified person discovery requires destructive approval", true)
			continue
		}
		if strings.TrimSpace(previousExtension.IdentityRelationField) != strings.TrimSpace(nextExtension.IdentityRelationField) || strings.TrimSpace(previousExtension.Cardinality) != strings.TrimSpace(nextExtension.Cardinality) {
			review.add("change_identity_profile_binding", path, "changing a Profile Extension Identity binding or cardinality requires destructive approval", true)
		}
		for _, tab := range nextExtension.ProfileTabs {
			if !containsTrimmedString(previousExtension.ProfileTabs, tab) {
				review.add("add_identity_profile_tab", path+".profile_tabs."+strings.TrimSpace(tab), "adding Profile tab "+strings.TrimSpace(tab), false)
			}
		}
		for _, tab := range previousExtension.ProfileTabs {
			if !containsTrimmedString(nextExtension.ProfileTabs, tab) {
				review.add("remove_identity_profile_tab", path+".profile_tabs."+strings.TrimSpace(tab), "removing a Profile tab requires destructive approval because fields may become undiscoverable", true)
			}
		}
		if !reflect.DeepEqual(previousExtension.ProfileTabLabels, nextExtension.ProfileTabLabels) ||
			!reflect.DeepEqual(previousExtension.ProfileTabFields, nextExtension.ProfileTabFields) ||
			!reflect.DeepEqual(previousExtension.ProfileTabRelatedObjects, nextExtension.ProfileTabRelatedObjects) ||
			!reflect.DeepEqual(previousExtension.ProfileTabComponents, nextExtension.ProfileTabComponents) {
			destructive := profileTabContractRemoves(previousExtension, nextExtension)
			description := "extending Profile tab labels, fields, related objects, or components"
			if destructive {
				description = "removing or replacing Profile tab labels, fields, related objects, or components requires destructive approval"
			}
			review.add("change_identity_profile_tab_contract", path+".profile_tab_contract", description, destructive)
		}
		if !reflect.DeepEqual(previousExtension.SummaryFields, nextExtension.SummaryFields) {
			review.add("change_identity_profile_summary", path+".summary_fields", "changing Profile summary fields", false)
		}
		if hasNewString(previousExtension.RequiredPermissions, nextExtension.RequiredPermissions) ||
			(previousExtension.DefaultVisibility == "hidden" && nextExtension.DefaultVisibility != "hidden") ||
			(!previousExtension.StandaloneWorkspace && nextExtension.StandaloneWorkspace) {
			review.add("widen_identity_profile_visibility", path+".visibility", "widening Profile discovery, permissions, or standalone navigation requires destructive approval", true)
		} else if !reflect.DeepEqual(previousExtension.RequiredPermissions, nextExtension.RequiredPermissions) || previousExtension.DefaultVisibility != nextExtension.DefaultVisibility || previousExtension.StandaloneWorkspace != nextExtension.StandaloneWorkspace {
			review.add("restrict_identity_profile_visibility", path+".visibility", "restricting Profile discovery or navigation", false)
		}
	}
	for objectKey := range next {
		if _, exists := previous[objectKey]; !exists {
			review.add("add_identity_profile_extension", "identity_profile_extensions."+objectKey, "adding Profile Extension "+objectKey, false)
		}
	}
}

func profileTabContractRemoves(previous profilebindingmodel.Binding, next profilebindingmodel.Binding) bool {
	for tab, label := range previous.ProfileTabLabels {
		if nextLabel, exists := next.ProfileTabLabels[tab]; !exists || nextLabel != label {
			return true
		}
	}
	for _, mappings := range []struct {
		previous map[string][]string
		next     map[string][]string
	}{
		{previous.ProfileTabFields, next.ProfileTabFields},
		{previous.ProfileTabRelatedObjects, next.ProfileTabRelatedObjects},
		{previous.ProfileTabComponents, next.ProfileTabComponents},
	} {
		for tab, values := range mappings.previous {
			nextValues, exists := mappings.next[tab]
			if !exists || hasNewString(values, nextValues) {
				return true
			}
		}
	}
	return false
}

func identityProfileExtensionMap(values []profilebindingmodel.Binding) map[string]profilebindingmodel.Binding {
	out := map[string]profilebindingmodel.Binding{}
	for _, extension := range values {
		if key := strings.TrimSpace(extension.ObjectKey); key != "" {
			out[key] = extension
		}
	}
	return out
}

func containsTrimmedString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func ValidateManifestUpdate(previous manifestmodel.ManifestSchema, next manifestmodel.ManifestSchema, opts ReviewOptions) error {
	if err := ValidateManifest(next); err != nil {
		return err
	}
	review := ReviewManifestUpdate(previous, next, opts)
	if !review.HasBlockers() {
		return nil
	}
	errs := ValidationErrors{}
	for _, blocker := range review.Blockers {
		errs = append(errs, ValidationError{Path: blocker.Path, Message: blocker.Description})
	}
	return errs
}

type manifestReview struct {
	previous manifestmodel.ManifestSchema
	next     manifestmodel.ManifestSchema
	opts     ReviewOptions
	result   ReviewResult
}

func (review *manifestReview) add(kind string, path string, description string, destructive bool) {
	change := ReviewChange{
		Kind:        kind,
		Path:        path,
		Description: description,
		Destructive: destructive,
		Approved:    !destructive || review.opts.ApproveDestructive,
	}
	review.result.Changes = append(review.result.Changes, change)
	if destructive && !review.opts.ApproveDestructive {
		review.result.Blockers = append(review.result.Blockers, change)
	}
}

func (review *manifestReview) reviewObjects() {
	prevObjects := objectMap(review.previous)
	nextObjects := objectMap(review.next)
	for objectKey, prevObject := range prevObjects {
		nextObject, exists := nextObjects[objectKey]
		if !exists {
			review.add("remove_object", "objects."+objectKey, "removing an object requires destructive approval", true)
			continue
		}
		review.reviewFields(objectKey, prevObject, nextObject)
	}
	for objectKey, nextObject := range nextObjects {
		if _, exists := prevObjects[objectKey]; !exists {
			review.add("add_object", "objects."+objectKey, "adding object "+objectKey, false)
			review.reviewNewRequiredFields(objectKey, nextObject)
		}
	}
}

func (review *manifestReview) reviewFields(objectKey string, previous definitionmodel.ObjectSchema, next definitionmodel.ObjectSchema) {
	prevFields := fieldMap(previous)
	nextFields := fieldMap(next)
	for fieldKey, prevField := range prevFields {
		nextField, exists := nextFields[fieldKey]
		path := fmt.Sprintf("objects.%s.fields.%s", objectKey, fieldKey)
		if !exists {
			review.add("remove_field", path, "removing a field requires destructive approval", true)
			continue
		}
		if strings.TrimSpace(prevField.Type) != strings.TrimSpace(nextField.Type) {
			review.add("change_field_type", path, "changing field type requires destructive approval", true)
		}
		if !prevField.Required && nextField.Required && nextField.Default == nil && nextField.DefaultValue == nil {
			review.add("add_required_without_default", path, "making an existing field required without a default requires destructive approval", true)
		}
	}
	for fieldKey, nextField := range nextFields {
		if _, exists := prevFields[fieldKey]; exists {
			continue
		}
		path := fmt.Sprintf("objects.%s.fields.%s", objectKey, fieldKey)
		if nextField.Required && nextField.Default == nil && nextField.DefaultValue == nil {
			review.add("add_required_without_default", path, "adding a required field without a default requires destructive approval", true)
		} else {
			review.add("add_field", path, "adding field "+fieldKey, false)
		}
	}
}

func (review *manifestReview) reviewNewRequiredFields(objectKey string, object definitionmodel.ObjectSchema) {
	for _, field := range object.Fields {
		if !field.Required || field.Default != nil || field.DefaultValue != nil {
			continue
		}
		path := fmt.Sprintf("objects.%s.fields.%s", objectKey, field.Key)
		review.add("add_required_without_default", path, "adding a required field without a default requires destructive approval", true)
	}
}
