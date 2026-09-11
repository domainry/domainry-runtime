package agenthost

import (
	"context"
	"sort"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

type businessRelation struct {
	agentsdk.ConversationBusinessRelation
	field string
}

func businessRelations(snapshot appschemamodel.ApplicationSchemaSnapshot, p principalmodel.Principal, objectKey string) []businessRelation {
	readable := map[string]bool{}
	for _, object := range snapshot.Objects {
		readable[object.Key] = definitionmodel.EffectiveObjectCapabilities(object).Read && recordpolicy.RecordAllowsObjectAction(p, object.Key, "read")
	}
	if !readable[objectKey] {
		return nil
	}
	var out []businessRelation
	for _, object := range snapshot.Objects {
		if !readable[object.Key] {
			continue
		}
		for _, field := range object.Fields {
			if field.Type != "relation" || field.DisabledAt != "" || !recordpolicy.RecordCanReadObjectFieldForPrincipal(p, object, field) || !businessOperatorAllowed(businessField(field, object.Key, p), "eq") {
				continue
			}
			target := recordvalidation.RecordRelationTarget(field)
			if !readable[target] {
				continue
			}
			if object.Key == objectKey {
				out = append(out, businessRelation{ConversationBusinessRelation: agentsdk.ConversationBusinessRelation{Key: "forward:" + field.Key, Label: field.Name, Direction: "forward", ObjectKey: target}, field: field.Key})
			}
			if target == objectKey {
				out = append(out, businessRelation{ConversationBusinessRelation: agentsdk.ConversationBusinessRelation{Key: "reverse:" + object.Key + ":" + field.Key, Label: object.Name + " / " + field.Name, Direction: "reverse", ObjectKey: object.Key}, field: field.Key})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func businessRelationCatalog(snapshot appschemamodel.ApplicationSchemaSnapshot, p principalmodel.Principal, q agentsdk.ConversationBusinessCatalogQuery, out agentsdk.ConversationBusinessCatalogPage) (agentsdk.ConversationBusinessCatalogPage, error) {
	for _, relation := range businessRelations(snapshot, p, q.ObjectKey) {
		if relation.Key <= q.After {
			continue
		}
		if len(out.Relations) == q.Limit {
			out.Complete = false
			out.NextCursor = out.Relations[len(out.Relations)-1].Key
			break
		}
		out.Relations = append(out.Relations, relation.ConversationBusinessRelation)
	}
	return out, nil
}

func (h *ConversationBusinessHost) QueryRelatedBusinessRecords(ctx context.Context, q agentsdk.ConversationBusinessRelatedQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRelatedPage, error) {
	out := agentsdk.ConversationBusinessRelatedPage{SourceObjectKey: q.ObjectKey, SourceRecordID: q.RecordID, RelationKey: q.RelationKey}
	if strings.TrimSpace(q.RecordID) == "" || len(q.RecordID) > 256 || q.RelationKey == "" || len(q.RelationKey) > 512 || q.PageSize < 0 || q.PageSize > 25 || len(q.Filters) > 19 {
		return out, conversationBusinessError("bad_request")
	}
	object, p, err := h.object(ctx, q.ObjectKey, a)
	if err != nil {
		return out, err
	}
	var selected *businessRelation
	for _, relation := range businessRelations(h.schema.ForPrincipal(ctx, p), p, object.Key) {
		if relation.Key == q.RelationKey {
			selected = &relation
			break
		}
	}
	if selected == nil {
		return out, conversationBusinessError("forbidden")
	}
	// Always read the source through the ordinary row/field policy boundary.
	// Knowing a source ID alone cannot be used to query its children.
	parent, err := h.records.GetRecord(ctx, object.Key, q.RecordID, p)
	if err != nil {
		return out, conversationBusinessReadError(err)
	}
	if parent.Deleted {
		return out, conversationBusinessError("not_found")
	}
	required := recordmodel.RecordFilterExpression{Field: selected.field, Operator: "eq", Value: q.RecordID}
	if selected.Direction == "forward" {
		value := parent.Data[selected.field]
		id, ok := value.(string)
		if value != nil && (!ok || len(id) > 256) {
			return out, conversationBusinessError("unavailable")
		}
		// NULL/unset references deliberately match no real record. Still run the
		// target query to enforce fields/filters and target access consistently.
		required = recordmodel.RecordFilterExpression{Field: "id", Operator: "eq", Value: id}
		if id == "" {
			required = recordmodel.RecordFilterExpression{Field: "id", Operator: "is_null"}
		}
	}
	query := agentsdk.ConversationBusinessQuery{ObjectKey: selected.ObjectKey, Fields: q.Fields, Filters: q.Filters, Sort: q.Sort, PageSize: q.PageSize, Cursor: q.Cursor}
	// Bind continuation to the relationship as well as the target query. This
	// is a position check, never a grant; current permissions apply every page.
	scope := conversationBusinessDigest([]any{q.ObjectKey, q.RecordID, q.RelationKey, selected.ConversationBusinessRelation, required})
	out.ConversationBusinessRecordPage, err = h.queryBusinessRecords(ctx, query, a, &required, scope)
	return out, err
}

var _ agentsdk.ConversationBusinessRelationSource = (*ConversationBusinessHost)(nil)
