package agenthost

import (
	"context"
	"encoding/json"
	"slices"
	"sort"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	invocationcontract "github.com/domainry/domainry-runtime/runtime/domain/invocation/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func (h *ConversationBusinessHost) BusinessCatalog(ctx context.Context, q agentsdk.ConversationBusinessCatalogQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessCatalogPage, error) {
	return h.businessCatalogAccess(ctx, q, a, nil)
}

func (h *ConversationBusinessHost) businessCatalogAccess(ctx context.Context, q agentsdk.ConversationBusinessCatalogQuery, a agentsdk.ConversationAuthority, receiptProducer *principalmodel.Principal) (agentsdk.ConversationBusinessCatalogPage, error) {
	out := agentsdk.ConversationBusinessCatalogPage{Items: []agentsdk.ConversationBusinessObject{}, Complete: true}
	p, err := h.principal(ctx, a)
	if err != nil {
		return out, err
	}
	if q.Limit == 0 {
		q.Limit = 10
	}
	if q.Kind == "" {
		q.Kind = "objects"
	}
	if q.Limit < 1 || q.Limit > 25 || len(q.After) > 2048 || len(q.ObjectKey) > 128 || len(q.WorkflowKey) > 128 || len(q.ActionKey) > 128 || !q.ValidSelector() {
		return out, conversationBusinessError("bad_request")
	}
	// The ordinary principal schema also includes create/update-only objects.
	// Keep them discoverable, but never pass them through the record read path.
	snapshot := h.schema.ForPrincipal(ctx, p)
	visible := map[string]bool{}
	for _, object := range snapshot.Objects {
		visible[object.Key] = true
	}
	if q.ObjectKey != "" && !visible[q.ObjectKey] {
		return out, conversationBusinessError("forbidden")
	}
	if q.Kind == "actions" {
		return h.businessActionCatalog(ctx, snapshot, p, q, out, receiptProducer)
	}
	if q.Kind == "workflows" {
		return h.businessWorkflowCatalog(ctx, snapshot, p, visible, q, out, receiptProducer)
	}
	if q.Kind == "relations" {
		return businessRelationCatalog(snapshot, p, q, out)
	}
	objects := append([]definitionmodel.ObjectSchema(nil), snapshot.Objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	for _, object := range objects {
		if object.Key <= q.After || q.ObjectKey != "" && object.Key != q.ObjectKey {
			continue
		}
		if len(out.Items) == q.Limit {
			out.Complete = false
			out.NextCursor = out.Items[len(out.Items)-1].Key
			break
		}
		readable := definitionmodel.EffectiveObjectCapabilities(object).Read && recordpolicy.RecordAllowsObjectAction(p, object.Key, "read")
		item := agentsdk.ConversationBusinessObject{Key: object.Key, Label: object.Name, Readable: &readable}
		if readable {
			item.Pagination = "cursor"
		}
		if q.ObjectKey != "" {
			if readable {
				for _, field := range object.Fields {
					if field.DisabledAt != "" || !recordpolicy.RecordCanReadObjectFieldForPrincipal(p, object, field) {
						continue
					}
					item.Fields = append(item.Fields, businessField(field, object.Key, p))

				}
			}
		}

		if q.ObjectKey != "" && readable {
			relations, _ := businessRelationCatalog(snapshot, p, agentsdk.ConversationBusinessCatalogQuery{ObjectKey: object.Key, Limit: 10}, out)
			item.Relations, item.RelationsNextCursor = relations.Relations, relations.NextCursor
		}

		// Do not hash undisclosed schema fields or handler implementation data.
		item.Version = conversationBusinessDigest(item)
		out.Items = append(out.Items, item)
	}
	if q.ObjectKey != "" && len(out.Items) == 0 {
		return out, conversationBusinessError("forbidden")
	}
	return out, nil
}

func (h *ConversationBusinessHost) businessActionCatalog(ctx context.Context, snapshot appschemamodel.ApplicationSchemaSnapshot, p principalmodel.Principal, q agentsdk.ConversationBusinessCatalogQuery, out agentsdk.ConversationBusinessCatalogPage, receiptProducer *principalmodel.Principal) (agentsdk.ConversationBusinessCatalogPage, error) {
	actions := append([]definitionmodel.ActionSchema(nil), snapshot.Actions...)
	var reader conversationBusinessActionCatalogReceiptReader
	if receiptProducer != nil {
		var ok bool
		reader, ok = h.actions.(conversationBusinessActionCatalogReceiptReader)
		if !ok {
			return out, &agentsdk.Error{Class: "unavailable", Code: agentsdk.BusinessResultReadUnsupportedCode}
		}
		actions = append([]definitionmodel.ActionSchema(nil), h.actions.Definitions()...)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Key < actions[j].Key })
	for _, action := range actions {
		if action.Key <= q.After || q.ActionKey != "" && action.Key != q.ActionKey || q.ObjectKey != "" && action.ObjectKey != q.ObjectKey {
			continue
		}
		if receiptProducer != nil {
			actual, err := reader.AgentSharedActionReceiptDefinition(ctx, action.Key, p, *receiptProducer)
			if err != nil || actual.Key != action.Key || actual.ObjectKey != action.ObjectKey {
				continue
			}
			action = actual
		} else if len(invocationcontract.ValidateActionPermission(action, p)) != 0 {
			continue
		}
		if len(out.Actions) == q.Limit {
			out.Complete = false
			out.NextCursor = out.Actions[len(out.Actions)-1].Key
			break
		}
		item := agentsdk.ConversationBusinessOperation{Key: action.Key, Label: action.Label, ObjectKeys: []string{action.ObjectKey}}
		if q.ActionKey != "" {
			if actual, ok := h.businessActionDefinition(action.Key); ok && actual.PayloadFields != nil && len(h.evidenceKey) > 0 {
				action = actual
				item.ExecutionVersion = h.businessActionVersion(action)
				item.Kind, item.OptimisticConcurrency, item.ConcurrencyField = action.Kind, action.OptimisticConcurrency, action.ConcurrencyField
			}
		}
		if q.ActionKey != "" && action.PayloadFields != nil {
			fields := action.PayloadFields
			if item.ExecutionVersion != "" {
				fields = businessActionPayloadFields(action)
			}
			raw, err := json.Marshal(invocationcontract.PayloadJSONSchema(fields, action.Defaults))
			if err != nil {
				return out, conversationBusinessError("unavailable")
			}
			item.InputSchema = raw
		}
		item.Version = conversationBusinessDigest(item)
		out.Actions = append(out.Actions, item)
	}
	if q.ActionKey != "" && len(out.Actions) == 0 {
		return out, conversationBusinessError("forbidden")
	}
	return out, nil
}

func (h *ConversationBusinessHost) businessWorkflowCatalog(ctx context.Context, snapshot appschemamodel.ApplicationSchemaSnapshot, p principalmodel.Principal, visible map[string]bool, q agentsdk.ConversationBusinessCatalogQuery, out agentsdk.ConversationBusinessCatalogPage, receiptProducer *principalmodel.Principal) (agentsdk.ConversationBusinessCatalogPage, error) {
	var reader conversationBusinessWorkflowCatalogReceiptReader
	if receiptProducer != nil {
		var ok bool
		reader, ok = h.workflows.(conversationBusinessWorkflowCatalogReceiptReader)
		if !ok {
			return out, &agentsdk.Error{Class: "unavailable", Code: agentsdk.BusinessResultReadUnsupportedCode}
		}
	}
	workflows := append([]definitionmodel.WorkflowSchema(nil), snapshot.Workflows...)
	sort.Slice(workflows, func(i, j int) bool { return workflows[i].Key < workflows[j].Key })
	for _, workflow := range workflows {
		if workflow.Key <= q.After || q.WorkflowKey != "" && workflow.Key != q.WorkflowKey {
			continue
		}
		// A mounted execution service is authoritative for both discovery and
		// detail. Do not mix stale schema labels/objects with a current contract.
		executionVersion := ""
		if h.workflows != nil && len(h.evidenceKey) > 0 {
			var actual definitionmodel.WorkflowSchema
			var err error
			if receiptProducer != nil {
				actual, err = reader.AgentSharedWorkflowReceiptDefinition(ctx, workflow.Key, p, *receiptProducer)
			} else {
				actual, err = h.workflows.AgentWorkflowDefinition(ctx, workflow.Key, p)
			}
			if err != nil || actual.Key != workflow.Key {
				continue
			}
			workflow = actual
			executionVersion = h.businessWorkflowVersion(actual)
		}
		// SchemaForPrincipal does not filter workflows. Apply the same target
		// mode and exact run permission used by the actual invocation boundary.
		if len(invocationcontract.ValidateWorkflowTarget(workflow, invocationcontract.WorkflowEntryAgent)) != 0 || receiptProducer == nil && len(invocationcontract.ValidateWorkflowPermission(workflow, p)) != 0 {
			continue
		}
		keys := workflowpolicy.WorkflowTriggerObjectKeys(workflow)
		if q.ObjectKey != "" && !slices.Contains(keys, q.ObjectKey) {
			continue
		}
		if len(out.Workflows) == q.Limit {
			out.Complete = false
			out.NextCursor = out.Workflows[len(out.Workflows)-1].Key
			break
		}
		item := agentsdk.ConversationBusinessOperation{Key: workflow.Key, Label: workflow.Name}
		if q.WorkflowKey != "" {
			item.ExecutionVersion = executionVersion
			raw, err := json.Marshal(invocationcontract.WorkflowPayloadJSONSchema(workflow))
			if err != nil {
				return out, conversationBusinessError("unavailable")
			}
			item.InputSchema = raw
		}
		for _, key := range keys {
			if visible[key] {
				item.ObjectKeys = append(item.ObjectKeys, key)
			}
		}
		sort.Strings(item.ObjectKeys)
		item.Version = conversationBusinessDigest(item)
		out.Workflows = append(out.Workflows, item)
	}
	if q.WorkflowKey != "" && len(out.Workflows) == 0 {
		return out, conversationBusinessError("forbidden")
	}
	return out, nil
}
