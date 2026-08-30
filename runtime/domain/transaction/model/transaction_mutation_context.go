package transactionmodel

import (
	"fmt"
	"slices"
	"strings"
)

type MutationSource string

const (
	MutationSourceHTTP        MutationSource = "http"
	MutationSourceAction      MutationSource = "action"
	MutationSourceWorkflow    MutationSource = "workflow"
	MutationSourceAutomation  MutationSource = "automation"
	MutationSourceImport      MutationSource = "import"
	MutationSourceScheduler   MutationSource = "scheduler"
	MutationSourceIntegration MutationSource = "integration"
	MutationSourceInternal    MutationSource = "internal"
)

type MutationContextInput struct {
	WorkspaceID               string
	ActorID                   string
	RoleKey                   string
	Permissions               []string
	DataScope                 string
	IdentityVersion           string
	Source                    MutationSource
	ActionKey                 string
	WorkflowKey               string
	AutomationKey             string
	RequestID                 string
	IdempotencyKey            string
	CorrelationID             string
	CausationID               string
	ApplicationSchemaRevision string
	EffectAuthority           map[string][]string
	AssuranceEvidence         map[string]string
}

type MutationContext struct {
	workspaceID       string
	actorID           string
	roleKey           string
	permissions       []string
	dataScope         string
	identityVersion   string
	source            MutationSource
	actionKey         string
	workflowKey       string
	automationKey     string
	requestID         string
	idempotencyKey    string
	correlationID     string
	causationID       string
	metadataRevision  string
	effectAuthority   map[string][]string
	assuranceEvidence map[string]string
}

type MutationContextError struct {
	Code  string
	Field string
}

func (e *MutationContextError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Field)
}

func NewMutationContext(input MutationContextInput) (MutationContext, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.RoleKey = strings.TrimSpace(input.RoleKey)
	input.DataScope = strings.TrimSpace(input.DataScope)
	input.IdentityVersion = strings.TrimSpace(input.IdentityVersion)
	input.ActionKey = strings.TrimSpace(input.ActionKey)
	input.WorkflowKey = strings.TrimSpace(input.WorkflowKey)
	input.AutomationKey = strings.TrimSpace(input.AutomationKey)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.CausationID = strings.TrimSpace(input.CausationID)
	input.ApplicationSchemaRevision = strings.TrimSpace(input.ApplicationSchemaRevision)
	if input.WorkspaceID == "" {
		return MutationContext{}, mutationContextError("workspace_id")
	}
	if !mutationSourceValid(input.Source) {
		return MutationContext{}, mutationContextError("source")
	}
	if input.CorrelationID == "" {
		return MutationContext{}, mutationContextError("correlation_id")
	}
	if input.ApplicationSchemaRevision == "" {
		return MutationContext{}, mutationContextError("metadata_revision")
	}
	for source, key := range map[MutationSource]string{
		MutationSourceAction: input.ActionKey, MutationSourceWorkflow: input.WorkflowKey, MutationSourceAutomation: input.AutomationKey,
	} {
		if input.Source == source && key == "" {
			return MutationContext{}, mutationContextError(string(source) + "_key")
		}
	}
	return MutationContext{
		workspaceID: input.WorkspaceID, actorID: input.ActorID, roleKey: input.RoleKey,
		permissions: mutationNormalizedStrings(input.Permissions), dataScope: input.DataScope, identityVersion: input.IdentityVersion,
		source: input.Source, actionKey: input.ActionKey, workflowKey: input.WorkflowKey, automationKey: input.AutomationKey,
		requestID: input.RequestID, idempotencyKey: input.IdempotencyKey, correlationID: input.CorrelationID,
		causationID: input.CausationID, metadataRevision: input.ApplicationSchemaRevision,
		effectAuthority: mutationCloneAuthority(input.EffectAuthority), assuranceEvidence: mutationCloneEvidence(input.AssuranceEvidence),
	}, nil
}

func (c MutationContext) WorkspaceID() string               { return c.workspaceID }
func (c MutationContext) ActorID() string                   { return c.actorID }
func (c MutationContext) RoleKey() string                   { return c.roleKey }
func (c MutationContext) Permissions() []string             { return slices.Clone(c.permissions) }
func (c MutationContext) DataScope() string                 { return c.dataScope }
func (c MutationContext) IdentityVersion() string           { return c.identityVersion }
func (c MutationContext) Source() MutationSource            { return c.source }
func (c MutationContext) ActionKey() string                 { return c.actionKey }
func (c MutationContext) WorkflowKey() string               { return c.workflowKey }
func (c MutationContext) AutomationKey() string             { return c.automationKey }
func (c MutationContext) RequestID() string                 { return c.requestID }
func (c MutationContext) IdempotencyKey() string            { return c.idempotencyKey }
func (c MutationContext) CorrelationID() string             { return c.correlationID }
func (c MutationContext) CausationID() string               { return c.causationID }
func (c MutationContext) ApplicationSchemaRevision() string { return c.metadataRevision }

func (c MutationContext) EffectAuthority() map[string][]string {
	return mutationCloneAuthority(c.effectAuthority)
}

func (c MutationContext) AssuranceEvidence() map[string]string {
	return mutationCloneEvidence(c.assuranceEvidence)
}

func (c MutationContext) AllowsEffect(objectKey, fieldKey string) bool {
	objectKey, fieldKey = strings.TrimSpace(objectKey), strings.TrimSpace(fieldKey)
	for _, allowed := range c.effectAuthority[objectKey] {
		if allowed == "*" || allowed == fieldKey {
			return true
		}
	}
	return false
}

func (c MutationContext) HasEffectAuthority() bool {
	return len(c.effectAuthority) > 0
}

func mutationSourceValid(source MutationSource) bool {
	switch source {
	case MutationSourceHTTP, MutationSourceAction, MutationSourceWorkflow, MutationSourceAutomation, MutationSourceImport, MutationSourceScheduler, MutationSourceIntegration, MutationSourceInternal:
		return true
	default:
		return false
	}
}

func mutationContextError(field string) error {
	return &MutationContextError{Code: "backend.mutation.context_invalid", Field: field}
}

func mutationNormalizedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func mutationCloneAuthority(values map[string][]string) map[string][]string {
	result := make(map[string][]string, len(values))
	for objectKey, fields := range values {
		objectKey = strings.TrimSpace(objectKey)
		if objectKey == "" {
			continue
		}
		result[objectKey] = mutationNormalizedStrings(fields)
	}
	return result
}

func mutationCloneEvidence(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		if key = strings.TrimSpace(key); key != "" {
			result[key] = strings.TrimSpace(value)
		}
	}
	return result
}
