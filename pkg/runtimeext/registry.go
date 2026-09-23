package runtimeext

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	notificationcontract "github.com/domainry/domainry-notification-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

var (
	ErrProjectExtensionRegistryFrozen = errors.New("project extension registry is frozen")
	ErrBusinessHandlerRequired        = errors.New("business handler is required")
	ErrBusinessHandlerDuplicate       = errors.New("business handler is already registered")
)

const (
	ProjectExtensionKindBusinessHandler    = "business_handler"
	ProjectExtensionKindAssigneeResolver   = "workflow_assignee_resolver"
	ProjectExtensionKindWorkspaceBootstrap = "workspace_bootstrap"
	ProjectExtensionKindProjectDefinition  = "project_definition"
)

// ProjectExtensions is the complete project-owned composition input.
// Runtime validates and freezes every extension before accepting traffic.
type ProjectExtensions struct {
	BusinessHandlers              []BusinessHandler
	AssigneeResolvers             []AssigneeResolver
	WorkspaceBootstrapParticipant WorkspaceBootstrapParticipant
	Definitions                   ProjectDefinitions
}

// ProjectDefinitions is the sole code-owned source for executable product
// behavior. None of these definitions are accepted from model.json.
type ProjectDefinitions struct {
	Reports                []ReportDefinition
	PublicResources        []PublicResourceDefinition
	Workflows              []definitionmodel.WorkflowSchema
	BusinessCalendars      []businesscalendarmodel.BusinessCalendarSchema
	Schedules              []schedulersdk.Definition
	AutomationRules        []automationmodel.AutomationRuleSchema
	NotificationTemplates  []notificationcontract.NotificationTemplate
	NotificationEventTypes []notificationcontract.NotificationEventType
	NotificationRules      []notificationcontract.NotificationRule
	IntegrationMappings    []integrationsdk.EventMappingRequirement
	AgentSkills            []agentsdk.SkillSchema
	Agents                 []agentsdk.AgentSchema
	AgentTasks             []agentsdk.AgentTaskDefinition
	AgentEntrypoints       []agentsdk.AgentEntrypointAssignment
	AgentServicePrincipals []agentsdk.AgentServicePrincipalBinding
}

// PublicResourceDefinition binds one anonymous, revocable projection to its
// storage object. The behavior is code-owned; model.json remains limited to
// storage shape and authorization.
type PublicResourceDefinition struct {
	ObjectKey string                               `json:"object_key"`
	Resource  definitionmodel.ObjectPublicResource `json:"resource"`
}

// ReportDefinition is the code-owned, immutable definition for one Report.
// Runtime owns this registration shape and translates it to the Report SDK at
// the module boundary so external projects do not need an unreleased Report SDK
// merely to compile runtimeext.
type ReportDefinition struct {
	Report                 reportmodel.ReportSchema
	OperationStateExamples []reportmodel.ReportOperationStateExampleSchema
	SensitiveFieldPolicies []reportmodel.ReportSensitiveFieldPolicySchema
	ExportControls         []reportmodel.ReportExportControlSchema
}

type ProjectDefinitionIdentity struct {
	Kind   string
	Key    string
	SHA256 string
}

// ProjectExtensionDescriptor is the canonical release-identity envelope for
// one executable project extension. Exactly one typed descriptor is populated
// according to Kind; generic payloads are deliberately unsupported.
type ProjectExtensionDescriptor struct {
	Kind               string
	Key                string
	BusinessHandler    *HandlerDescriptor
	AssigneeResolver   *AssigneeResolverDescriptor
	WorkspaceBootstrap *WorkspaceBootstrapDescriptor
	ProjectDefinition  *ProjectDefinitionIdentity
}

// BusinessHandlerBinding is the immutable registration-time association
// between one published Action identity and its source-owned Handler.
type BusinessHandlerBinding struct {
	Descriptor HandlerDescriptor
	Handler    BusinessHandler
}

// ProjectExtensionRegistry owns startup-time project extension registration.
// It intentionally has no runtime replacement or unregister operation.
type ProjectExtensionRegistry struct {
	mu                           sync.RWMutex
	frozen                       bool
	businessHandlerBindings      map[string]BusinessHandlerBinding
	assigneeResolverBindings     map[string]AssigneeResolverBinding
	workspaceBootstrap           WorkspaceBootstrapParticipant
	workspaceBootstrapDescriptor *WorkspaceBootstrapDescriptor
	definitions                  frozenProjectDefinitions
}

func NewProjectExtensionRegistry() *ProjectExtensionRegistry {
	return &ProjectExtensionRegistry{businessHandlerBindings: map[string]BusinessHandlerBinding{}, assigneeResolverBindings: map[string]AssigneeResolverBinding{}, definitions: newFrozenProjectDefinitions()}
}

func (r *ProjectExtensionRegistry) RegisterBusinessHandler(handler BusinessHandler) error {
	if handler == nil {
		return ErrBusinessHandlerRequired
	}
	descriptor := normalizeHandlerDescriptor(handler.Descriptor())
	if err := descriptor.Validate(); err != nil {
		return err
	}
	key := strings.TrimSpace(descriptor.ActionKey)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrProjectExtensionRegistryFrozen
	}
	if r.businessHandlerBindings == nil {
		r.businessHandlerBindings = map[string]BusinessHandlerBinding{}
	}
	if _, exists := r.businessHandlerBindings[key]; exists {
		return fmt.Errorf("%w: %s", ErrBusinessHandlerDuplicate, key)
	}
	r.businessHandlerBindings[key] = BusinessHandlerBinding{Descriptor: descriptor, Handler: handler}
	return nil
}

func (r *ProjectExtensionRegistry) RegisterAssigneeResolver(resolver AssigneeResolver) error {
	return r.RegisterProjectExtensions(ProjectExtensions{AssigneeResolvers: []AssigneeResolver{resolver}})
}

func (r *ProjectExtensionRegistry) RegisterProjectExtensions(set ProjectExtensions) error {
	definitions, err := validateProjectDefinitions(set.Definitions)
	if err != nil {
		return err
	}
	var workspaceBootstrapDescriptor *WorkspaceBootstrapDescriptor
	if set.WorkspaceBootstrapParticipant != nil {
		descriptor := normalizeWorkspaceBootstrapDescriptor(set.WorkspaceBootstrapParticipant.Descriptor())
		if err := descriptor.Validate(); err != nil {
			return err
		}
		workspaceBootstrapDescriptor = &descriptor
	}
	type registration struct {
		key     string
		binding BusinessHandlerBinding
	}
	registrations := make([]registration, 0, len(set.BusinessHandlers))
	seen := map[string]bool{}
	for _, handler := range set.BusinessHandlers {
		if handler == nil {
			return ErrBusinessHandlerRequired
		}
		descriptor := normalizeHandlerDescriptor(handler.Descriptor())
		if err := descriptor.Validate(); err != nil {
			return err
		}
		key := strings.TrimSpace(descriptor.ActionKey)
		if seen[key] {
			return fmt.Errorf("%w: %s", ErrBusinessHandlerDuplicate, key)
		}
		seen[key] = true
		registrations = append(registrations, registration{key: key, binding: BusinessHandlerBinding{Descriptor: descriptor, Handler: handler}})
	}
	type resolverRegistration struct {
		key     string
		binding AssigneeResolverBinding
	}
	resolverRegistrations := make([]resolverRegistration, 0, len(set.AssigneeResolvers))
	seenResolvers := map[string]bool{}
	for _, resolver := range set.AssigneeResolvers {
		if resolver == nil {
			return ErrAssigneeResolverContractInvalid
		}
		descriptor := normalizeAssigneeResolverDescriptor(resolver.Descriptor())
		if err := descriptor.Validate(); err != nil {
			return err
		}
		key := strings.TrimSpace(descriptor.ResolverKey)
		if seenResolvers[key] {
			return fmt.Errorf("%w: %s", ErrAssigneeResolverDuplicate, key)
		}
		seenResolvers[key] = true
		resolverRegistrations = append(resolverRegistrations, resolverRegistration{key: key, binding: AssigneeResolverBinding{Descriptor: descriptor, Resolver: resolver}})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrProjectExtensionRegistryFrozen
	}
	if set.WorkspaceBootstrapParticipant != nil && r.workspaceBootstrap != nil {
		return ErrWorkspaceBootstrapParticipantDuplicate
	}
	if err := r.definitions.rejectDuplicates(definitions); err != nil {
		return err
	}
	if r.businessHandlerBindings == nil {
		r.businessHandlerBindings = map[string]BusinessHandlerBinding{}
	}
	if r.assigneeResolverBindings == nil {
		r.assigneeResolverBindings = map[string]AssigneeResolverBinding{}
	}
	for _, current := range registrations {
		if _, exists := r.businessHandlerBindings[current.key]; exists {
			return fmt.Errorf("%w: %s", ErrBusinessHandlerDuplicate, current.key)
		}
	}
	for _, current := range resolverRegistrations {
		if _, exists := r.assigneeResolverBindings[current.key]; exists {
			return fmt.Errorf("%w: %s", ErrAssigneeResolverDuplicate, current.key)
		}
	}
	for _, current := range registrations {
		r.businessHandlerBindings[current.key] = current.binding
	}
	for _, current := range resolverRegistrations {
		r.assigneeResolverBindings[current.key] = current.binding
	}
	if set.WorkspaceBootstrapParticipant != nil {
		r.workspaceBootstrap = set.WorkspaceBootstrapParticipant
		r.workspaceBootstrapDescriptor = workspaceBootstrapDescriptor
	}
	r.definitions.merge(definitions)
	return nil
}

func (r *ProjectExtensionRegistry) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

func (r *ProjectExtensionRegistry) Frozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

func (r *ProjectExtensionRegistry) BusinessHandlerBinding(actionKey string) (BusinessHandlerBinding, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	binding, ok := r.businessHandlerBindings[strings.TrimSpace(actionKey)]
	if ok {
		binding.Descriptor = cloneHandlerDescriptor(binding.Descriptor)
	}
	return binding, ok
}

func (r *ProjectExtensionRegistry) BusinessHandlerDescriptors() []HandlerDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]HandlerDescriptor, 0, len(r.businessHandlerBindings))
	for _, binding := range r.businessHandlerBindings {
		result = append(result, cloneHandlerDescriptor(binding.Descriptor))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ActionKey < result[j].ActionKey })
	return result
}

func (r *ProjectExtensionRegistry) AssigneeResolverBinding(resolverKey string) (AssigneeResolverBinding, bool) {
	if r == nil {
		return AssigneeResolverBinding{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	binding, ok := r.assigneeResolverBindings[strings.TrimSpace(resolverKey)]
	if ok {
		binding.Descriptor = cloneAssigneeResolverDescriptor(binding.Descriptor)
	}
	return binding, ok
}

func (r *ProjectExtensionRegistry) AssigneeResolverDescriptors() []AssigneeResolverDescriptor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]AssigneeResolverDescriptor, 0, len(r.assigneeResolverBindings))
	for _, binding := range r.assigneeResolverBindings {
		result = append(result, cloneAssigneeResolverDescriptor(binding.Descriptor))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ResolverKey < result[j].ResolverKey })
	return result
}

func (r *ProjectExtensionRegistry) Descriptors() []ProjectExtensionDescriptor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]ProjectExtensionDescriptor, 0, len(r.businessHandlerBindings)+len(r.assigneeResolverBindings)+len(r.definitions.identities)+1)
	for _, binding := range r.businessHandlerBindings {
		descriptor := cloneHandlerDescriptor(binding.Descriptor)
		result = append(result, ProjectExtensionDescriptor{
			Kind: ProjectExtensionKindBusinessHandler, Key: descriptor.ActionKey, BusinessHandler: &descriptor,
		})
	}
	for _, binding := range r.assigneeResolverBindings {
		descriptor := cloneAssigneeResolverDescriptor(binding.Descriptor)
		result = append(result, ProjectExtensionDescriptor{
			Kind: ProjectExtensionKindAssigneeResolver, Key: descriptor.ResolverKey, AssigneeResolver: &descriptor,
		})
	}
	if r.workspaceBootstrapDescriptor != nil {
		descriptor := cloneWorkspaceBootstrapDescriptor(*r.workspaceBootstrapDescriptor)
		result = append(result, ProjectExtensionDescriptor{
			Kind: ProjectExtensionKindWorkspaceBootstrap, Key: descriptor.Key, WorkspaceBootstrap: &descriptor,
		})
	}
	for _, identity := range r.definitions.identityList() {
		current := identity
		result = append(result, ProjectExtensionDescriptor{Kind: ProjectExtensionKindProjectDefinition, Key: identity.Kind + ":" + identity.Key, ProjectDefinition: &current})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Kind+"\x00"+result[i].Key < result[j].Kind+"\x00"+result[j].Key
	})
	return result
}

func (r *ProjectExtensionRegistry) WorkspaceBootstrapParticipant() WorkspaceBootstrapParticipant {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workspaceBootstrap
}

func normalizeAssigneeResolverDescriptor(descriptor AssigneeResolverDescriptor) AssigneeResolverDescriptor {
	result := cloneAssigneeResolverDescriptor(descriptor)
	result.ResolverKey = strings.TrimSpace(result.ResolverKey)
	result.ResolverRevision = strings.TrimSpace(result.ResolverRevision)
	result.ConfigContractSHA256 = strings.TrimSpace(result.ConfigContractSHA256)
	for index := range result.ConfigFields {
		result.ConfigFields[index].Key = strings.TrimSpace(result.ConfigFields[index].Key)
		sort.Strings(result.ConfigFields[index].Enum)
	}
	sort.Slice(result.ConfigFields, func(i, j int) bool { return result.ConfigFields[i].Key < result.ConfigFields[j].Key })
	for index := range result.RecordCapabilities {
		capability := &result.RecordCapabilities[index]
		capability.Key = strings.TrimSpace(capability.Key)
		capability.ObjectKey = strings.TrimSpace(capability.ObjectKey)
		for fieldIndex := range capability.Fields {
			capability.Fields[fieldIndex] = strings.TrimSpace(capability.Fields[fieldIndex])
		}
		for fieldIndex := range capability.FilterFields {
			capability.FilterFields[fieldIndex] = strings.TrimSpace(capability.FilterFields[fieldIndex])
		}
		sort.Strings(capability.Fields)
		sort.Strings(capability.FilterFields)
	}
	sort.Slice(result.RecordCapabilities, func(i, j int) bool { return result.RecordCapabilities[i].Key < result.RecordCapabilities[j].Key })
	for index := range result.RelationCapabilities {
		capability := &result.RelationCapabilities[index]
		capability.Key = strings.TrimSpace(capability.Key)
		capability.SourceObjectKey = strings.TrimSpace(capability.SourceObjectKey)
		capability.RelationFieldKey = strings.TrimSpace(capability.RelationFieldKey)
		capability.TargetObjectKey = strings.TrimSpace(capability.TargetObjectKey)
		for fieldIndex := range capability.TargetFields {
			capability.TargetFields[fieldIndex] = strings.TrimSpace(capability.TargetFields[fieldIndex])
		}
		sort.Strings(capability.TargetFields)
	}
	sort.Slice(result.RelationCapabilities, func(i, j int) bool { return result.RelationCapabilities[i].Key < result.RelationCapabilities[j].Key })
	for index := range result.IdentityProjections {
		result.IdentityProjections[index] = strings.TrimSpace(result.IdentityProjections[index])
	}
	sort.Strings(result.IdentityProjections)
	for index := range result.CandidateRoleKeys {
		result.CandidateRoleKeys[index] = strings.TrimSpace(result.CandidateRoleKeys[index])
	}
	sort.Strings(result.CandidateRoleKeys)
	return result
}

func cloneAssigneeResolverDescriptor(descriptor AssigneeResolverDescriptor) AssigneeResolverDescriptor {
	result := descriptor
	result.ConfigFields = make([]AssigneeResolverConfigField, len(descriptor.ConfigFields))
	for index, field := range descriptor.ConfigFields {
		result.ConfigFields[index] = field
		result.ConfigFields[index].Enum = append([]string(nil), field.Enum...)
	}
	result.RecordCapabilities = make([]AssigneeResolverRecordCapability, len(descriptor.RecordCapabilities))
	for index, capability := range descriptor.RecordCapabilities {
		result.RecordCapabilities[index] = capability
		result.RecordCapabilities[index].Fields = append([]string(nil), capability.Fields...)
		result.RecordCapabilities[index].FilterFields = append([]string(nil), capability.FilterFields...)
	}
	result.RelationCapabilities = make([]AssigneeResolverRelationCapability, len(descriptor.RelationCapabilities))
	for index, capability := range descriptor.RelationCapabilities {
		result.RelationCapabilities[index] = capability
		result.RelationCapabilities[index].TargetFields = append([]string(nil), capability.TargetFields...)
	}
	result.IdentityProjections = append([]string(nil), descriptor.IdentityProjections...)
	result.CandidateRoleKeys = append([]string(nil), descriptor.CandidateRoleKeys...)
	return result
}

func normalizeWorkspaceBootstrapDescriptor(descriptor WorkspaceBootstrapDescriptor) WorkspaceBootstrapDescriptor {
	result := cloneWorkspaceBootstrapDescriptor(descriptor)
	result.Key = strings.TrimSpace(result.Key)
	result.InputType = strings.TrimSpace(result.InputType)
	result.InputContractSHA256 = strings.TrimSpace(result.InputContractSHA256)
	result.ParticipantRevision = strings.TrimSpace(result.ParticipantRevision)
	for index := range result.InputFields {
		field := &result.InputFields[index]
		field.Key = strings.TrimSpace(field.Key)
		field.Pattern = strings.TrimSpace(field.Pattern)
		field.Format = strings.TrimSpace(field.Format)
		sort.Strings(field.Enum)
	}
	sort.Slice(result.InputFields, func(i, j int) bool { return result.InputFields[i].Key < result.InputFields[j].Key })
	for index := range result.Records {
		record := &result.Records[index]
		record.Key = strings.TrimSpace(record.Key)
		record.ObjectKey = strings.TrimSpace(record.ObjectKey)
		for fieldIndex := range record.Fields {
			record.Fields[fieldIndex] = strings.TrimSpace(record.Fields[fieldIndex])
		}
		sort.Strings(record.Fields)
	}
	sort.Slice(result.Records, func(i, j int) bool { return result.Records[i].Key < result.Records[j].Key })
	return result
}

func cloneWorkspaceBootstrapDescriptor(descriptor WorkspaceBootstrapDescriptor) WorkspaceBootstrapDescriptor {
	result := descriptor
	result.InputFields = make([]WorkspaceBootstrapInputField, len(descriptor.InputFields))
	for index, field := range descriptor.InputFields {
		result.InputFields[index] = field
		result.InputFields[index].Enum = append([]string(nil), field.Enum...)
		if field.Minimum != nil {
			value := *field.Minimum
			result.InputFields[index].Minimum = &value
		}
		if field.Maximum != nil {
			value := *field.Maximum
			result.InputFields[index].Maximum = &value
		}
		if field.MinLength != nil {
			value := *field.MinLength
			result.InputFields[index].MinLength = &value
		}
		if field.MaxLength != nil {
			value := *field.MaxLength
			result.InputFields[index].MaxLength = &value
		}
	}
	result.Records = make([]WorkspaceBootstrapRecordCapability, len(descriptor.Records))
	for index, record := range descriptor.Records {
		result.Records[index] = record
		result.Records[index].Fields = append([]string(nil), record.Fields...)
	}
	return result
}

func normalizeHandlerDescriptor(descriptor HandlerDescriptor) HandlerDescriptor {
	result := cloneHandlerDescriptor(descriptor)
	result.ActionKey = strings.TrimSpace(result.ActionKey)
	result.ObjectKey = strings.TrimSpace(result.ObjectKey)
	result.Label = strings.TrimSpace(result.Label)
	result.Kind = strings.TrimSpace(result.Kind)
	result.RiskLevel = strings.TrimSpace(result.RiskLevel)
	result.AuditEvent = strings.TrimSpace(result.AuditEvent)
	result.InputType = strings.TrimSpace(result.InputType)
	result.OutputType = strings.TrimSpace(result.OutputType)
	result.ConcurrencyField = strings.TrimSpace(result.ConcurrencyField)
	result.HandlerRevision = strings.TrimSpace(result.HandlerRevision)
	for index := range result.ObjectCapabilities {
		capability := &result.ObjectCapabilities[index]
		capability.ObjectKey = strings.TrimSpace(capability.ObjectKey)
		for operationIndex := range capability.Operations {
			capability.Operations[operationIndex] = strings.TrimSpace(capability.Operations[operationIndex])
		}
		sort.Strings(capability.Operations)
	}
	sort.Slice(result.ObjectCapabilities, func(i, j int) bool {
		return result.ObjectCapabilities[i].ObjectKey < result.ObjectCapabilities[j].ObjectKey
	})
	for index := range result.ConnectorCapabilities {
		capability := &result.ConnectorCapabilities[index]
		capability.ConnectorKey = strings.TrimSpace(capability.ConnectorKey)
		capability.ConnectionKey = strings.TrimSpace(capability.ConnectionKey)
		capability.OperationKey = strings.TrimSpace(capability.OperationKey)
	}
	sort.Slice(result.ConnectorCapabilities, func(i, j int) bool {
		left, right := result.ConnectorCapabilities[i], result.ConnectorCapabilities[j]
		return left.ConnectorKey+"\x00"+left.ConnectionKey+"\x00"+left.OperationKey < right.ConnectorKey+"\x00"+right.ConnectionKey+"\x00"+right.OperationKey
	})
	for index := range result.NotificationEventTypes {
		result.NotificationEventTypes[index] = strings.TrimSpace(result.NotificationEventTypes[index])
	}
	sort.Strings(result.NotificationEventTypes)
	for index := range result.FileCapabilities {
		result.FileCapabilities[index] = strings.TrimSpace(result.FileCapabilities[index])
	}
	sort.Strings(result.FileCapabilities)
	for index := range result.CrossWorkspaceAggregates {
		capability := &result.CrossWorkspaceAggregates[index]
		capability.Key = strings.TrimSpace(capability.Key)
		capability.ObjectKey = strings.TrimSpace(capability.ObjectKey)
		for dimensionIndex := range capability.Dimensions {
			capability.Dimensions[dimensionIndex].Key = strings.TrimSpace(capability.Dimensions[dimensionIndex].Key)
			capability.Dimensions[dimensionIndex].Field = strings.TrimSpace(capability.Dimensions[dimensionIndex].Field)
			if capability.Dimensions[dimensionIndex].Transform != nil {
				transform := *capability.Dimensions[dimensionIndex].Transform
				if transform.DateBucket != nil {
					dateBucket := *transform.DateBucket
					dateBucket.Grain = strings.TrimSpace(dateBucket.Grain)
					dateBucket.TimeZone = strings.TrimSpace(dateBucket.TimeZone)
					transform.DateBucket = &dateBucket
				}
				capability.Dimensions[dimensionIndex].Transform = &transform
			}
		}
		sort.Slice(capability.Dimensions, func(i, j int) bool { return capability.Dimensions[i].Key < capability.Dimensions[j].Key })
		for measureIndex := range capability.Measures {
			capability.Measures[measureIndex].Key = strings.TrimSpace(capability.Measures[measureIndex].Key)
			capability.Measures[measureIndex].Field = strings.TrimSpace(capability.Measures[measureIndex].Field)
		}
		sort.Slice(capability.Measures, func(i, j int) bool { return capability.Measures[i].Key < capability.Measures[j].Key })
		for filterIndex := range capability.Filters {
			capability.Filters[filterIndex].Field = strings.TrimSpace(capability.Filters[filterIndex].Field)
			for operatorIndex := range capability.Filters[filterIndex].Operators {
				capability.Filters[filterIndex].Operators[operatorIndex] = strings.TrimSpace(capability.Filters[filterIndex].Operators[operatorIndex])
			}
			sort.Strings(capability.Filters[filterIndex].Operators)
		}
		sort.Slice(capability.Filters, func(i, j int) bool { return capability.Filters[i].Field < capability.Filters[j].Field })
	}
	sort.Slice(result.CrossWorkspaceAggregates, func(i, j int) bool {
		return result.CrossWorkspaceAggregates[i].Key < result.CrossWorkspaceAggregates[j].Key
	})
	if result.TargetOrganization != nil {
		capability := *result.TargetOrganization
		capability.Source = TargetOrganizationSource(strings.TrimSpace(string(capability.Source)))
		capability.Input = strings.TrimSpace(capability.Input)
		result.TargetOrganization = &capability
	}
	if result.OrganizationUnitDelivery != nil {
		capability := *result.OrganizationUnitDelivery
		capability.Operations = append([]OrganizationUnitDeliveryOperation(nil), capability.Operations...)
		for index := range capability.Operations {
			capability.Operations[index] = OrganizationUnitDeliveryOperation(strings.TrimSpace(string(capability.Operations[index])))
		}
		sort.Slice(capability.Operations, func(i, j int) bool { return capability.Operations[i] < capability.Operations[j] })
		capability.NodeTypes = append([]OrganizationUnitNodeType(nil), capability.NodeTypes...)
		for index := range capability.NodeTypes {
			capability.NodeTypes[index] = OrganizationUnitNodeType(strings.TrimSpace(string(capability.NodeTypes[index])))
		}
		sort.Slice(capability.NodeTypes, func(i, j int) bool { return capability.NodeTypes[i] < capability.NodeTypes[j] })
		capability.ParentSource = OrganizationUnitParentSource(strings.TrimSpace(string(capability.ParentSource)))
		result.OrganizationUnitDelivery = &capability
	}
	if result.IdentityHandlerDelivery != nil {
		capability := *result.IdentityHandlerDelivery
		capability.InitialCredentialOutputField = strings.TrimSpace(capability.InitialCredentialOutputField)
		capability.Operations = append([]IdentityHandlerOperation(nil), capability.Operations...)
		for index := range capability.Operations {
			capability.Operations[index] = IdentityHandlerOperation(strings.TrimSpace(string(capability.Operations[index])))
		}
		sort.Slice(capability.Operations, func(i, j int) bool { return capability.Operations[i] < capability.Operations[j] })
		capability.ProfileBindings = append([]IdentityProfileBindingCapability(nil), capability.ProfileBindings...)
		for index := range capability.ProfileBindings {
			capability.ProfileBindings[index].BindingKey = strings.TrimSpace(capability.ProfileBindings[index].BindingKey)
			capability.ProfileBindings[index].ObjectKey = strings.TrimSpace(capability.ProfileBindings[index].ObjectKey)
		}
		sort.Slice(capability.ProfileBindings, func(i, j int) bool {
			left, right := capability.ProfileBindings[i], capability.ProfileBindings[j]
			return left.BindingKey+"\x00"+left.ObjectKey < right.BindingKey+"\x00"+right.ObjectKey
		})
		result.IdentityHandlerDelivery = &capability
	}
	if result.AccountErasure != nil {
		capability := *result.AccountErasure
		capability.Operations = append([]AccountErasureOperation(nil), capability.Operations...)
		sort.Slice(capability.Operations, func(i, j int) bool { return capability.Operations[i] < capability.Operations[j] })
		result.AccountErasure = &capability
	}
	if result.StoreOrganizationCatalog != nil {
		capability := *result.StoreOrganizationCatalog
		result.StoreOrganizationCatalog = &capability
	}
	if result.StoreOrganizationMutation != nil {
		capability := *result.StoreOrganizationMutation
		capability.Operations = append([]StoreOrganizationMutationOperation(nil), capability.Operations...)
		for index := range capability.Operations {
			capability.Operations[index] = StoreOrganizationMutationOperation(strings.TrimSpace(string(capability.Operations[index])))
		}
		sort.Slice(capability.Operations, func(i, j int) bool { return capability.Operations[i] < capability.Operations[j] })
		result.StoreOrganizationMutation = &capability
	}
	if result.WorkspaceIdentityUsage != nil {
		capability := *result.WorkspaceIdentityUsage
		result.WorkspaceIdentityUsage = &capability
	}
	return result
}

func cloneHandlerDescriptor(descriptor HandlerDescriptor) HandlerDescriptor {
	result := descriptor
	result.Preconditions = append([]string(nil), descriptor.Preconditions...)
	result.PayloadFields = cloneDefinition(descriptor.PayloadFields)
	result.OutputFields = cloneDefinition(descriptor.OutputFields)
	result.Defaults = cloneDefinition(descriptor.Defaults)
	if descriptor.AssurancePolicy != nil {
		policy := cloneDefinition(*descriptor.AssurancePolicy)
		result.AssurancePolicy = &policy
	}
	result.ObjectCapabilities = make([]ActionObjectCapability, len(descriptor.ObjectCapabilities))
	for index, capability := range descriptor.ObjectCapabilities {
		result.ObjectCapabilities[index] = ActionObjectCapability{ObjectKey: capability.ObjectKey, Operations: append([]string(nil), capability.Operations...)}
	}
	result.ConnectorCapabilities = append([]ActionConnectorCapability(nil), descriptor.ConnectorCapabilities...)
	result.NotificationEventTypes = append([]string(nil), descriptor.NotificationEventTypes...)
	result.FileCapabilities = append([]string(nil), descriptor.FileCapabilities...)
	if descriptor.CrossWorkspaceAggregates != nil {
		result.CrossWorkspaceAggregates = make([]CrossWorkspaceAggregateCapability, len(descriptor.CrossWorkspaceAggregates))
		for index, capability := range descriptor.CrossWorkspaceAggregates {
			result.CrossWorkspaceAggregates[index] = capability
			result.CrossWorkspaceAggregates[index].Dimensions = append([]CrossWorkspaceAggregateDimension(nil), capability.Dimensions...)
			for dimensionIndex := range result.CrossWorkspaceAggregates[index].Dimensions {
				dimension := &result.CrossWorkspaceAggregates[index].Dimensions[dimensionIndex]
				if dimension.Transform != nil {
					transform := *dimension.Transform
					if transform.DateBucket != nil {
						dateBucket := *transform.DateBucket
						transform.DateBucket = &dateBucket
					}
					dimension.Transform = &transform
				}
			}
			result.CrossWorkspaceAggregates[index].Measures = append([]CrossWorkspaceAggregateMeasure(nil), capability.Measures...)
			result.CrossWorkspaceAggregates[index].Filters = make([]CrossWorkspaceAggregateFilterCapability, len(capability.Filters))
			for filterIndex, filter := range capability.Filters {
				result.CrossWorkspaceAggregates[index].Filters[filterIndex] = CrossWorkspaceAggregateFilterCapability{Field: filter.Field, Operators: append([]string(nil), filter.Operators...)}
			}
		}
	}
	if descriptor.TargetOrganization != nil {
		capability := *descriptor.TargetOrganization
		result.TargetOrganization = &capability
	}
	if descriptor.OrganizationUnitDelivery != nil {
		capability := *descriptor.OrganizationUnitDelivery
		capability.Operations = append([]OrganizationUnitDeliveryOperation(nil), capability.Operations...)
		capability.NodeTypes = append([]OrganizationUnitNodeType(nil), capability.NodeTypes...)
		result.OrganizationUnitDelivery = &capability
	}
	if descriptor.IdentityHandlerDelivery != nil {
		capability := *descriptor.IdentityHandlerDelivery
		capability.Operations = append([]IdentityHandlerOperation(nil), capability.Operations...)
		capability.ProfileBindings = append([]IdentityProfileBindingCapability(nil), capability.ProfileBindings...)
		result.IdentityHandlerDelivery = &capability
	}
	if descriptor.AccountErasure != nil {
		capability := *descriptor.AccountErasure
		capability.Operations = append([]AccountErasureOperation(nil), capability.Operations...)
		result.AccountErasure = &capability
	}
	if descriptor.StoreOrganizationCatalog != nil {
		capability := *descriptor.StoreOrganizationCatalog
		result.StoreOrganizationCatalog = &capability
	}
	if descriptor.StoreOrganizationMutation != nil {
		capability := *descriptor.StoreOrganizationMutation
		capability.Operations = append([]StoreOrganizationMutationOperation(nil), capability.Operations...)
		result.StoreOrganizationMutation = &capability
	}
	if descriptor.WorkspaceIdentityUsage != nil {
		capability := *descriptor.WorkspaceIdentityUsage
		result.WorkspaceIdentityUsage = &capability
	}
	return result
}
