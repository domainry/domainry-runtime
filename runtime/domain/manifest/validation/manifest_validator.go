package validation

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	"fmt"
	"regexp"
	"sort"
	"strings"

	notificationsdkcontract "github.com/domainry/domainry-notification-sdk/contract"
)

var storageValuePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

type ValidationError struct {
	Path    string
	Message string
}

func (err ValidationError) Error() string {
	if strings.TrimSpace(err.Path) == "" {
		return err.Message
	}
	return err.Path + ": " + err.Message
}

type ValidationErrors []ValidationError

func (errs ValidationErrors) Error() string {
	if len(errs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func ValidateManifest(manifest manifestmodel.ManifestSchema) error {
	return ValidateManifestWithConnectorCatalog(manifest, nil)
}

func ValidateIdentityProfileBindings(objects []definitionmodel.ObjectSchema, bindings []profilebindingmodel.Binding) error {
	state := newValidationState(manifestmodel.ManifestSchema{Objects: objects, IdentityProfileExtensions: bindings}, nil)
	state.validateIdentityProfileExtensions()
	if len(state.errs) > 0 {
		return state.errs
	}
	return nil
}

// ValidateRuntimeDefinitionGraph validates the composed, active Runtime
// definition graph without re-validating build-time manifest envelope fields.
// Metadata drafts use this after applying every mutation in memory so
// references introduced or removed by the same draft are judged together.
func ValidateRuntimeDefinitionGraph(manifest manifestmodel.ManifestSchema) error {
	return ValidateRuntimeDefinitionGraphWithConnectorCatalog(manifest, nil)
}

// ValidateRuntimeDefinitionGraphWithConnectorCatalog validates the active
// definition graph against the same Integration-projected Connector catalog
// used by manifest bootstrap. Application requirements retain precedence in
// validationState without becoming Connector source definitions.
func ValidateRuntimeDefinitionGraphWithConnectorCatalog(manifest manifestmodel.ManifestSchema, connectorCatalog []connectormodel.ConnectorSchema) error {
	state := newValidationState(manifest, connectorCatalog)
	state.validateSchedulerOwnershipAndDefinitions()
	state.validateObjects()
	state.validateRoles()
	state.validateDictionaries()
	state.validateIdentityProfileExtensions()
	state.validateWorkspaceProvisioning()
	state.validateActions()
	state.validateAgents()
	state.validateIntegrationConnections()
	state.validateWorkflows()
	state.validateAutomationRules()
	state.validateMutationGraph()
	state.validateNotificationTemplates()
	state.validateReports()
	state.validateGovernance()
	state.validateRequiredIndexes()
	if len(state.errs) > 0 {
		return state.errs
	}
	return nil
}

// ValidateManifestWithConnectorCatalog validates a manifest against an
// explicitly composed Integration-projected Connector catalog. Manifest
// requirements take precedence over catalog entries with the same key.
func ValidateManifestWithConnectorCatalog(manifest manifestmodel.ManifestSchema, connectorCatalog []connectormodel.ConnectorSchema) error {
	state := newValidationState(manifest, connectorCatalog)
	state.validateRequiredShell()
	state.validateSourceIntentCoverage()
	state.validateSchedulerOwnershipAndDefinitions()
	state.validateObjects()
	state.validateRoles()
	state.validateDictionaries()
	state.validateIdentityProfileExtensions()
	state.validateWorkspaceProvisioning()
	state.validateActions()
	state.validateAgents()
	state.validateIntegrationConnections()
	state.validateWorkflows()
	state.validateAutomationRules()
	state.validateMutationGraph()
	state.validateNotificationTemplates()
	state.validateReports()
	state.validateGovernance()
	state.validateSeedRecords()
	state.validateRequiredIndexes()
	if len(state.errs) > 0 {
		return state.errs
	}
	return nil
}

func (state *validationState) validateNotificationTemplates() {
	if err := validateNotificationTemplates(state.manifest.NotificationTemplates); err != nil {
		state.add("notification_templates", "%v", err)
	}
	if err := notificationsdkcontract.ValidateEventTypes(state.manifest.NotificationEventTypes, state.manifest.NotificationRules); err != nil {
		state.add("notification_event_types", "%v", err)
	}
}

func (state *validationState) validateIdentityProfileExtensions() {
	seen := map[string]bool{}
	seenBindingKeys := map[string]bool{}
	for index, extension := range state.manifest.IdentityProfileExtensions {
		path := fmt.Sprintf("identity_profile_extensions[%d]", index)
		if extension.ContractVersion != profilebindingmodel.ContractVersion {
			state.add(path+".contract_version", "must be %q", profilebindingmodel.ContractVersion)
		}
		if extension.MinReaderVersion != profilebindingmodel.MinimumReaderVersion {
			state.add(path+".min_reader_version", "must be %q", profilebindingmodel.MinimumReaderVersion)
		}
		if strings.TrimSpace(extension.ObjectKey) == "" {
			state.add(path+".object_key", "is required")
		} else if seen[extension.ObjectKey] {
			state.add(path+".object_key", "duplicate extension for object %q", extension.ObjectKey)
		} else if _, ok := state.objects[extension.ObjectKey]; !ok {
			state.add(path+".object_key", "references unknown object %q", extension.ObjectKey)
		}
		seen[extension.ObjectKey] = true
		if strings.TrimSpace(extension.IdentityRelationField) == "" {
			state.add(path+".identity_relation_field", "is required")
		} else if object, ok := state.objects[extension.ObjectKey]; ok {
			field := state.fields[extension.ObjectKey][extension.IdentityRelationField]
			if strings.TrimSpace(field.Key) == "" {
				state.add(path+".identity_relation_field", "references unknown field %q", extension.IdentityRelationField)
			} else {
				if field.Type != "relation" {
					state.add(path+".identity_relation_field", "field %q must have type relation", extension.IdentityRelationField)
				}
				if target := runtimeRelationTargetObject(field); target != "identity_user" {
					state.add(path+".identity_relation_field", "field %q must target identity_user, got %q", extension.IdentityRelationField, target)
				}
				if !field.Unique {
					state.add(path+".identity_relation_field", "field %q must declare unique=true as one_to_one evidence", extension.IdentityRelationField)
				}
				if extension.BindingLifecycle.AllowUnbound && field.Required {
					state.add(path+".identity_relation_field", "field %q must be nullable when binding_lifecycle.allow_unbound=true", extension.IdentityRelationField)
				}
			}
			if strings.TrimSpace(fmt.Sprint(object.UX["kind"])) != "identity_profile_extension" {
				state.add(path+".object_key", "object %q must retain ux.kind identity_profile_extension", extension.ObjectKey)
			}
			for fieldKey := range state.fields[extension.ObjectKey] {
				if owner, owned := profilebindingmodel.ReservedFieldOwnerFor(fieldKey); owned {
					state.add(path+".object_key", "object %q duplicates Runtime %s-owned field %q", extension.ObjectKey, owner, fieldKey)
				}
			}
		}
		if extension.Cardinality != "one_to_one" {
			state.add(path+".cardinality", "only one_to_one is supported in %s", profilebindingmodel.ContractVersion)
		}
		state.validateBusinessIdentityBinding(path+".business_identity", extension)
		state.validateIdentityProfileBindingLifecycle(path+".binding_lifecycle", extension)
		bindingKey := strings.TrimSpace(extension.BusinessIdentity.Key)
		if bindingKey != "" && seenBindingKeys[bindingKey] {
			state.add(path+".business_identity.key", "duplicate business identity binding %q", bindingKey)
		}
		seenBindingKeys[bindingKey] = true
		switch extension.DefaultVisibility {
		case "when_readable", "hidden":
		default:
			state.add(path+".default_visibility", "must be when_readable or hidden")
		}
		validateRuntimeProfileFieldRefs(state, path+".summary_fields", extension.ObjectKey, extension.SummaryFields)
		tabs := map[string]bool{}
		for _, tab := range extension.ProfileTabs {
			tabs[tab] = true
		}
		for tab := range extension.ProfileTabLabels {
			if !tabs[tab] {
				state.add(path+".profile_tab_labels."+tab, "references undeclared profile tab")
			}
		}
		for tab, fields := range extension.ProfileTabFields {
			if !tabs[tab] {
				state.add(path+".profile_tab_fields."+tab, "references undeclared profile tab")
			}
			validateRuntimeProfileFieldRefs(state, path+".profile_tab_fields."+tab, extension.ObjectKey, fields)
		}
		for tab, objectKeys := range extension.ProfileTabRelatedObjects {
			if !tabs[tab] {
				state.add(path+".profile_tab_related_objects."+tab, "references undeclared profile tab")
			}
			for _, objectKey := range objectKeys {
				if _, ok := state.objects[objectKey]; !ok {
					state.add(path+".profile_tab_related_objects."+tab, "references unknown object %q", objectKey)
				}
			}
		}
		seenPermissions := map[string]bool{}
		for permissionIndex, permission := range extension.RequiredPermissions {
			permission = strings.TrimSpace(permission)
			if permission == "" || seenPermissions[permission] {
				state.add(fmt.Sprintf("%s.required_permissions[%d]", path, permissionIndex), "must be a unique non-empty Identity Catalog permission")
			}
			seenPermissions[permission] = true
		}
	}
}

func (state *validationState) validateIdentityProfileBindingLifecycle(path string, extension profilebindingmodel.Binding) {
	lifecycle := extension.BindingLifecycle
	seenChannels := map[string]bool{}
	for _, channel := range lifecycle.InvitationChannels {
		channel = strings.TrimSpace(channel)
		if !map[string]bool{"email": true, "sms": true, "external_idp": true}[channel] {
			state.add(path+".invitation_channels", "contains unsupported channel %q", channel)
		} else if seenChannels[channel] {
			state.add(path+".invitation_channels", "contains duplicate channel %q", channel)
		}
		seenChannels[channel] = true
	}
	seenProofs := map[string]bool{}
	for index, proof := range lifecycle.ClaimProofs {
		proofPath := fmt.Sprintf("%s.claim_proofs[%d]", path, index)
		proofType := strings.TrimSpace(proof.Type)
		if !map[string]bool{"email": true, "phone": true, "external_idp_subject": true}[proofType] {
			state.add(proofPath+".type", "must be email, phone or external_idp_subject")
		}
		if seenProofs[proofType] && proofType != "" {
			state.add(proofPath+".type", "duplicate claim proof type %q", proofType)
		}
		seenProofs[proofType] = true
		fieldKey := strings.TrimSpace(proof.FieldKey)
		if fieldKey == "" {
			state.add(proofPath+".field_key", "is required")
		} else if state.fields[extension.ObjectKey][fieldKey].Key == "" {
			state.add(proofPath+".field_key", "references unknown profile field %q", fieldKey)
		}
	}
	if lifecycle.AllowUnbound && len(lifecycle.ClaimProofs) == 0 {
		state.add(path+".claim_proofs", "must declare at least one server-verifiable proof when unbound profiles are allowed")
	}
	if !lifecycle.AllowUnbound && (len(lifecycle.InvitationChannels) > 0 || len(lifecycle.ClaimProofs) > 0) {
		state.add(path+".allow_unbound", "must be true when invitation channels or claim proofs are declared")
	}
}

func runtimeRelationTargetObject(field definitionmodel.FieldSchema) string {
	for _, key := range []string{"object_key", "target", "target_object"} {
		if target := mapString(field.Config, key); target != "" {
			return target
		}
	}
	return strings.TrimSpace(field.Validation.Target)
}

func validateRuntimeProfileFieldRefs(state *validationState, path, objectKey string, fieldKeys []string) {
	for _, fieldKey := range fieldKeys {
		if state.fields[objectKey][fieldKey].Key == "" {
			state.add(path, "references unknown profile field %q", fieldKey)
		}
	}
}

func (state *validationState) validateSourceIntentCoverage() {
	coverage := state.manifest.SourceIntentCoverage
	if coverage == nil {
		return
	}
	if strings.TrimSpace(coverage.Version) == "" {
		state.add("source_intent_coverage.version", "is required")
	}
	if len(coverage.UnmappedPaths) > 0 {
		state.add("source_intent_coverage.unmapped_paths", "must be empty before Runtime Provision: %s", strings.Join(coverage.UnmappedPaths, ", "))
	}
	seen := map[string]bool{}
	for index, entry := range coverage.Entries {
		path := fmt.Sprintf("source_intent_coverage.entries[%d]", index)
		if strings.TrimSpace(entry.SourcePath) == "" {
			state.add(path+".source_path", "is required")
		} else if seen[entry.SourcePath] {
			state.add(path+".source_path", "duplicate source path %q", entry.SourcePath)
		}
		seen[entry.SourcePath] = true
		switch entry.Status {
		case "compiled", "normalized", "composed", "deferred", "rejected":
		default:
			state.add(path+".status", "unsupported source intent disposition %q", entry.Status)
		}
		if strings.TrimSpace(entry.Target) == "" {
			state.add(path+".target", "is required")
		}
	}
}

type validationState struct {
	manifest   manifestmodel.ManifestSchema
	errs       ValidationErrors
	objects    map[string]definitionmodel.ObjectSchema
	actions    map[string]definitionmodel.ActionSchema
	fields     map[string]map[string]definitionmodel.FieldSchema
	dicts      map[string]appschemamodel.DictionarySchema
	connectors map[string]connectormodel.ConnectorSchema
	seeds      map[string]string
	seeded     map[string]bool
}

func newValidationState(manifest manifestmodel.ManifestSchema, connectorCatalog []connectormodel.ConnectorSchema) *validationState {
	state := &validationState{
		manifest:   manifest,
		objects:    map[string]definitionmodel.ObjectSchema{},
		actions:    map[string]definitionmodel.ActionSchema{},
		fields:     map[string]map[string]definitionmodel.FieldSchema{},
		dicts:      map[string]appschemamodel.DictionarySchema{},
		connectors: map[string]connectormodel.ConnectorSchema{},
		seeds:      map[string]string{},
		seeded:     map[string]bool{},
	}
	for _, connector := range connectorCatalog {
		if key := strings.TrimSpace(connector.Key); key != "" {
			state.connectors[key] = connector
		}
	}
	for _, object := range manifest.Objects {
		key := strings.TrimSpace(object.Key)
		if key == "" {
			continue
		}
		state.objects[key] = object
		state.fields[key] = map[string]definitionmodel.FieldSchema{}
		for _, field := range object.Fields {
			if fieldKey := strings.TrimSpace(field.Key); fieldKey != "" {
				state.fields[key][fieldKey] = field
			}
		}
	}
	for _, action := range manifest.Actions {
		if key := strings.TrimSpace(action.Key); key != "" {
			state.actions[key] = action
		}
	}
	for _, dict := range manifest.Dictionaries {
		if key := strings.TrimSpace(dict.Key); key != "" {
			state.dicts[key] = dict
		}
	}
	for _, connector := range manifest.Integrations.Connectors {
		if key := strings.TrimSpace(connector.Key); key != "" {
			state.connectors[key] = connector
		}
	}
	for index, seed := range manifest.SeedRecords {
		key := seedKey(seed, index)
		if key != "" {
			state.seeds[key] = strings.TrimSpace(seed.ObjectKey)
		}
		if objectKey := strings.TrimSpace(seed.ObjectKey); objectKey != "" {
			state.seeded[objectKey] = true
		}
	}
	return state
}

func (state *validationState) add(path string, format string, args ...any) {
	state.errs = append(state.errs, ValidationError{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (state *validationState) validateRequiredShell() {
	if version := strings.TrimSpace(state.manifest.SchemaVersion); version != "" && version != manifestmodel.CurrentManifestSchemaVersion {
		state.add("schema_version", "must be %q", manifestmodel.CurrentManifestSchemaVersion)
	}
	if strings.TrimSpace(state.manifest.TemplateID) == "" {
		state.add("template_id", "is required")
	}
	if strings.TrimSpace(state.manifest.Version) == "" {
		state.add("version", "is required")
	}
	if len(state.manifest.Objects) == 0 {
		state.add("objects", "at least one object is required")
	}
}

func seedKey(seed businessseedmodel.SeedRecordSchema, index int) string {
	if seed.Data != nil {
		key := strings.TrimSpace(fmt.Sprint(seed.Data["__seed_key"]))
		if key != "" && key != "<nil>" {
			return key
		}
	}
	objectKey := strings.TrimSpace(seed.ObjectKey)
	if objectKey == "" {
		return ""
	}
	return fmt.Sprintf("%s_seed_%03d", objectKey, index+1)
}

func stableOptionValue(value string) bool {
	return storageValuePattern.MatchString(strings.TrimSpace(value))
}

func mapString(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	raw, ok := value[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}
