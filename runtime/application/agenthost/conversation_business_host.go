package agenthost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

type ConversationBusinessSchema interface {
	ForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
}
type ConversationBusinessRecords interface {
	ListRecords(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
	GetRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
}

// ConversationBusinessHost uses the ordinary Runtime read boundary, including
// its row scope, field policies and masking. It never obtains a Task credential
// or an internal Action/system principal on behalf of a conversation.
type ConversationBusinessHost struct {
	runtimeID   string
	application identitysdk.ApplicationScope
	principals  identitysdk.PrincipalResolver
	schema      ConversationBusinessSchema
	records     ConversationBusinessRecords
	source      string
	evidenceKey []byte
	actions     ConversationBusinessActions
	workflows   ConversationBusinessWorkflows
	reports     ConversationBusinessReports
}

type ConversationBusinessHostOption func(*ConversationBusinessHost) error

// WithConversationBusinessIdentityIssuer binds saved sources and proofs to the
// actual Identity trust domain. Names shared by independent Identity deployments
// must not make their users or previously authorized evidence interchangeable.
func WithConversationBusinessIdentityIssuer(issuer string) ConversationBusinessHostOption {
	return func(h *ConversationBusinessHost) error {
		if issuer == "" || strings.TrimSpace(issuer) != issuer || len(issuer) > 2048 || strings.ContainsAny(issuer, "\r\n\x00") {
			return fmt.Errorf("conversation business Identity issuer is required")
		}
		h.source = "runtime-business-v2:" + conversationBusinessDigest([]string{h.runtimeID, string(h.application.WorkspaceID), string(h.application.ApplicationKey), issuer})
		return nil
	}
}

func NewConversationBusinessHost(runtimeID string, application identitysdk.ApplicationScope, principals identitysdk.PrincipalResolver, schema ConversationBusinessSchema, records ConversationBusinessRecords, options ...ConversationBusinessHostOption) (*ConversationBusinessHost, error) {
	if strings.TrimSpace(runtimeID) == "" || application.WorkspaceID == "" || application.ApplicationKey == "" || principals == nil || schema == nil || records == nil {
		return nil, fmt.Errorf("conversation business host dependencies are incomplete")
	}
	host := &ConversationBusinessHost{runtimeID: runtimeID, application: application, principals: principals, schema: schema, records: records,
		source: "runtime-business-v1:" + conversationBusinessDigest([]string{runtimeID, string(application.WorkspaceID), string(application.ApplicationKey)})}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("conversation business host option is nil")
		}
		if err := option(host); err != nil {
			return nil, err
		}
	}
	return host, nil
}

func conversationBusinessDigest(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func conversationBusinessError(class string) error {
	return &agentsdk.Error{Class: class, Code: "runtime.conversation.business_" + class}
}
func conversationBusinessReadError(err error) error {
	if err == nil {
		return nil
	}
	switch apperror.KindOf(err) {
	case apperror.KindForbidden:
		return conversationBusinessError("forbidden")
	case apperror.KindNotFound:
		return conversationBusinessError("not_found")
	case apperror.KindBadRequest, apperror.KindConflict:
		return conversationBusinessError("bad_request")
	default:
		return conversationBusinessError("unavailable")
	}
}

func (h *ConversationBusinessHost) principal(ctx context.Context, a agentsdk.ConversationAuthority) (principalmodel.Principal, error) {
	if !a.Known || a.RuntimeID != h.runtimeID || a.WorkspaceID != string(h.application.WorkspaceID) || strings.TrimSpace(a.UserID) == "" {
		return principalmodel.Principal{}, conversationBusinessError("forbidden")
	}
	resolved, err := h.principals.Resolve(ctx, identitysdk.PrincipalResolutionRequest{Application: h.application, SubjectID: identitysdk.SubjectID(a.UserID), RoleKey: a.RoleKey})
	if err != nil {
		return principalmodel.Principal{}, conversationBusinessReadError(err)
	}
	p := resolved.Principal
	if !p.Known || p.UserID != a.UserID || p.WorkspaceID != a.WorkspaceID {
		return principalmodel.Principal{}, conversationBusinessError("forbidden")
	}
	p.AccessBundle = &resolved.AccessBundle
	return principalmodel.NewPrincipalFromIdentity(p, ""), nil
}

func (h *ConversationBusinessHost) authorizeAction(ctx context.Context, a agentsdk.ConversationAuthority, key string) (agentsdk.ConversationToolAuthorization, error) {
	var out agentsdk.ConversationToolAuthorization
	p, err := h.principal(ctx, a)
	if err != nil {
		return out, err
	}
	separator := strings.LastIndexByte(key, '.')
	if separator < 1 {
		return out, nil
	}
	decision, err := evaluator.Evaluate(*p.AccessBundle, identitysdk.AccessRequest{ObjectKey: key[:separator], Action: key[separator+1:]}, identitysdk.ResourceFacts{"owner_user_id": a.UserID, "workspace_id": a.WorkspaceID}, time.Now().UTC())
	if err != nil {
		return out, conversationBusinessReadError(err)
	}
	out.Granted, out.Revision, out.UserTimezone = decision.Allowed, string(p.AccessBundle.AuthorizationRevision), p.User.Timezone
	return out, nil
}
func (h *ConversationBusinessHost) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	if (in.Definition.Key == "workflow_start" || in.Definition.Key == "workflow_get") && (h.workflows == nil || len(h.evidenceKey) == 0) {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	definitions := append(agentsdk.PersonalConversationTools(), agentsdk.ArtifactConversationTools()...)
	definitions = append(definitions, agentsdk.KnowledgeConversationTools()...)
	definitions = append(definitions, agentsdk.BusinessConversationTools()...)
	definitions = append(definitions, agentsdk.BusinessRelationConversationTools()...)
	definitions = append(definitions, agentsdk.BusinessActionConversationTools()...)
	definitions = append(definitions, agentsdk.BusinessWorkflowConversationTools()...)
	definitions = append(definitions, toolsdk.ReportQueryDefinitions()...)
	definitions = append(definitions, toolsdk.AnalysisDefinitions()...)
	for _, definition := range definitions {
		if definition.Key == in.Definition.Key && definition.ActionKey == in.Definition.ActionKey && definition.Version == in.Definition.Version {
			return h.authorizeAction(ctx, in.Authority, definition.ActionKey)
		}
	}
	return agentsdk.ConversationToolAuthorization{}, nil
}
func (h *ConversationBusinessHost) AuthorizeConversationInteraction(ctx context.Context, a agentsdk.ConversationAuthority, _ agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	return h.authorizeAction(ctx, a, agentsdk.ConversationInteractionPermission().Key)
}

func (h *ConversationBusinessHost) AuthorizeConversationExecution(ctx context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
	_, err := h.principal(ctx, in.Authority)
	var denied *agentsdk.Error
	if errors.As(err, &denied) && (denied.Class == "forbidden" || denied.Class == "not_found") {
		return false, nil
	}
	return err == nil, err
}

var _ agentsdk.ConversationExecutionAuthorizer = (*ConversationBusinessHost)(nil)

func (h *ConversationBusinessHost) BusinessSourceIdentity() string { return h.source }

func (h *ConversationBusinessHost) objects(ctx context.Context, p principalmodel.Principal) []definitionmodel.ObjectSchema {
	snapshot := h.schema.ForPrincipal(ctx, p)
	objects := make([]definitionmodel.ObjectSchema, 0, len(snapshot.Objects))
	for _, object := range snapshot.Objects {
		if !definitionmodel.EffectiveObjectCapabilities(object).Read || !recordpolicy.RecordAllowsObjectAction(p, object.Key, "read") {
			continue
		}
		fields := make([]definitionmodel.FieldSchema, 0, len(object.Fields))
		for _, field := range object.Fields {
			if field.DisabledAt == "" && recordpolicy.RecordCanReadObjectFieldForPrincipal(p, object, field) {
				fields = append(fields, field)
			}
		}
		object.Fields = fields
		objects = append(objects, object)
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	return objects
}

func businessField(field definitionmodel.FieldSchema, object string, p principalmodel.Principal) agentsdk.ConversationBusinessField {
	out := agentsdk.ConversationBusinessField{Key: field.Key, Label: field.Name, Type: field.Type}
	if recordpolicy.RecordFieldReadMaskedForPrincipal(p, object, field.Key) || recordpolicy.RecordFieldRequiresPolicyEvaluation(p, object, field.Key, "read") {
		return out
	}
	switch field.Type {
	case "text", "long_text", "email", "phone", "url", "select", "relation", "date", "datetime", "integer", "number", "currency", "percent", "boolean":
		out.FilterOperators = []string{"eq", "ne", "in", "not_in", "is_null", "is_not_null"}
		out.Sortable = true
	default:
		return out
	}
	switch field.Type {
	case "integer", "number", "currency", "percent", "date", "datetime":
		out.FilterOperators = append(out.FilterOperators, "gt", "gte", "lt", "lte")
	case "text", "long_text", "email", "phone", "url":
		out.FilterOperators = append(out.FilterOperators, "contains")
	}
	return out
}

func (h *ConversationBusinessHost) object(ctx context.Context, key string, a agentsdk.ConversationAuthority) (definitionmodel.ObjectSchema, principalmodel.Principal, error) {
	p, err := h.principal(ctx, a)
	if err != nil {
		return definitionmodel.ObjectSchema{}, p, err
	}
	for _, object := range h.objects(ctx, p) {
		if object.Key == key {
			return object, p, nil
		}
	}
	return definitionmodel.ObjectSchema{}, p, conversationBusinessError("forbidden")
}

func businessSelectFields(object definitionmodel.ObjectSchema, requested []string) ([]string, error) {
	if len(requested) > 50 {
		return nil, conversationBusinessError("bad_request")
	}
	allowed := map[string]bool{}
	for _, field := range object.Fields {
		allowed[field.Key] = true
	}
	if len(requested) == 0 {
		for _, field := range object.Fields {
			requested = append(requested, field.Key)
		}
	}
	seen := map[string]bool{}
	for _, key := range requested {
		if !allowed[key] || seen[key] {
			return nil, conversationBusinessError("bad_request")
		}
		seen[key] = true
	}
	return requested, nil
}

func businessRecord(record recordmodel.Record, fields []string) (agentsdk.ConversationBusinessRecord, error) {
	out := agentsdk.ConversationBusinessRecord{ID: record.ID, Version: record.UpdatedAt, Data: map[string]json.RawMessage{}}
	for _, key := range fields {
		if value, ok := record.Data[key]; ok {
			raw, err := json.Marshal(value)
			if err != nil {
				return out, conversationBusinessError("unavailable")
			}
			out.Data[key] = raw
		}
	}
	return out, nil
}

func (h *ConversationBusinessHost) GetBusinessRecord(ctx context.Context, q agentsdk.ConversationBusinessGet, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecord, error) {
	var out agentsdk.ConversationBusinessRecord
	if q.RecordID == "" || len(q.RecordID) > 256 {
		return out, conversationBusinessError("bad_request")
	}
	object, p, err := h.object(ctx, q.ObjectKey, a)
	if err != nil {
		return out, err
	}
	fields, err := businessSelectFields(object, q.Fields)
	if err != nil {
		return out, err
	}
	record, err := h.records.GetRecord(ctx, object.Key, q.RecordID, p)
	if err != nil {
		return out, conversationBusinessReadError(err)
	}
	if record.Deleted {
		return out, conversationBusinessError("not_found")
	}
	return businessRecord(record, fields)
}

func (h *ConversationBusinessHost) RevalidateBusiness(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	if e.Version != 1 || e.Source != h.source || e.ScopeSHA256 != conversationBusinessDigest([]string{h.source, a.RuntimeID, a.WorkspaceID, a.UserID}) {
		return conversationBusinessError("forbidden")
	}
	if e.HostProof != "" {
		return h.revalidateBusinessSnapshot(ctx, e, a)
	}
	return h.revalidateBusinessExact(ctx, e, a)
}

func (h *ConversationBusinessHost) revalidateBusinessExact(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	var current any
	var err error
	switch e.Operation {
	case "business_catalog":
		var q agentsdk.ConversationBusinessCatalogQuery
		if json.Unmarshal(e.Input, &q) != nil {
			return conversationBusinessError("bad_request")
		}
		current, err = h.BusinessCatalog(ctx, q, a)
	case "query_records":
		var q agentsdk.ConversationBusinessQuery
		if json.Unmarshal(e.Input, &q) != nil {
			return conversationBusinessError("bad_request")
		}
		current, err = h.QueryBusinessRecords(ctx, q, a)
	case "query_related_records":
		var q agentsdk.ConversationBusinessRelatedQuery
		if json.Unmarshal(e.Input, &q) != nil {
			return conversationBusinessError("bad_request")
		}
		current, err = h.QueryRelatedBusinessRecords(ctx, q, a)
	case "get_record":
		var q agentsdk.ConversationBusinessGet
		if json.Unmarshal(e.Input, &q) != nil {
			return conversationBusinessError("bad_request")
		}
		current, err = h.GetBusinessRecord(ctx, q, a)
	default:
		return conversationBusinessError("forbidden")
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(current)
	if err != nil || !bytes.Equal(raw, e.Data) {
		return conversationBusinessError("forbidden")
	}
	return nil
}

var _ agentsdk.ConversationBusinessSource = (*ConversationBusinessHost)(nil)
var _ agentsdk.ConversationToolAuthorizer = (*ConversationBusinessHost)(nil)
var _ agentsdk.ConversationInteractionAuthorizer = (*ConversationBusinessHost)(nil)

// Keep operator checks tied to the same catalog projection used by the model.
func businessOperatorAllowed(field agentsdk.ConversationBusinessField, operator string) bool {
	return slices.Contains(field.FilterOperators, operator)
}
