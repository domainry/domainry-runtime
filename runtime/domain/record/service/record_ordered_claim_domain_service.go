package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type RecordOrderedClaimRequest struct {
	WorkspaceID      string
	Object           definitionmodel.ObjectSchema
	Query            recordmodel.RecordListQuery
	StatusField      string
	EligibleStatuses []string
	ClaimedStatus    string
	PriorityField    string
	SequenceField    string
	ClaimPatch       map[string]any
	Now              time.Time
}

// RecordClaimFirstEligible atomically claims the first eligible row using a
// deterministic priority/sequence/created_at/id order. A compare-and-set on
// the observed status closes concurrent selection races; losers retry from the
// head instead of claiming an out-of-order row from a stale page.
func RecordClaimFirstEligible(ctx context.Context, repository recordrepository.RecordRepository, request RecordOrderedClaimRequest) (recordmodel.Record, bool, error) {
	request.StatusField = strings.TrimSpace(request.StatusField)
	request.ClaimedStatus = strings.TrimSpace(request.ClaimedStatus)
	request.PriorityField = valueOrDefaultString(strings.TrimSpace(request.PriorityField), "priority")
	request.SequenceField = valueOrDefaultString(strings.TrimSpace(request.SequenceField), "sequence")
	if repository == nil || strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.Object.Key) == "" || request.StatusField == "" || request.ClaimedStatus == "" || len(request.EligibleStatuses) == 0 {
		return recordmodel.Record{}, false, fmt.Errorf("ordered claim contract is incomplete")
	}
	if request.Now.IsZero() {
		request.Now = time.Now().UTC()
	}
	eligible := make([]any, 0, len(request.EligibleStatuses))
	for _, status := range request.EligibleStatuses {
		if status = strings.TrimSpace(status); status != "" {
			eligible = append(eligible, status)
		}
	}
	if len(eligible) == 0 {
		return recordmodel.Record{}, false, fmt.Errorf("ordered claim eligible statuses are empty")
	}
	query := request.Query
	query.Page, query.PageSize = 1, 1
	query.AuthorizationMode = recordmodel.RecordQueryAuthorizationUnrestricted
	if query.Filters == nil {
		query.Filters = map[string]any{}
	}
	query.Filters[request.StatusField+"__in"] = eligible
	query.Sort = []recordmodel.RecordSortRule{{Field: request.PriorityField, Direction: "desc"}, {Field: request.SequenceField, Direction: "asc"}, {Field: "created_at", Direction: "asc"}, {Field: "id", Direction: "asc"}}
	for attempt := 0; attempt < 32; attempt++ {
		page, err := repository.ListRecords(ctx, request.WorkspaceID, request.Object, query)
		if err != nil {
			return recordmodel.Record{}, false, err
		}
		if len(page.Items) == 0 {
			return recordmodel.Record{}, false, nil
		}
		candidate := page.Items[0]
		observedStatus := strings.TrimSpace(fmt.Sprint(candidate.Data[request.StatusField]))
		data := make(map[string]any, len(candidate.Data)+len(request.ClaimPatch)+1)
		for key, value := range candidate.Data {
			data[key] = value
		}
		for key, value := range request.ClaimPatch {
			if key = strings.TrimSpace(key); key != "" {
				data[key] = value
			}
		}
		data[request.StatusField] = request.ClaimedStatus
		candidate.Data = data
		candidate.UpdatedAt = request.Now.UTC().Format(time.RFC3339Nano)
		claimed, err := repository.UpdateRecordWhere(ctx, request.WorkspaceID, request.Object, candidate, map[string]any{request.StatusField: observedStatus})
		if err != nil {
			return recordmodel.Record{}, false, err
		}
		if claimed {
			return candidate, true, nil
		}
	}
	return recordmodel.Record{}, false, fmt.Errorf("ordered claim contention exceeded retry budget")
}

func valueOrDefaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
