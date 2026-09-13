package runtime

import (
	"context"
	"fmt"

	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	auditrepository "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	subjectevidence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/subjectevidence"
)

// Discover source event IDs through Audit's public read port. Runtime does not
// query another owner's tables or guess subjects from arbitrary payload text.
func subjectEvidenceEvents(repository auditrepository.AuditRepository) subjectevidence.EventResolver {
	return func(ctx context.Context, workspace, subject string, resources []recordmodel.SubjectRecordReference) ([]string, error) {
		queries := []auditcontract.AuditEventQuery{{ActorID: subject, Limit: 500}}
		for _, resource := range resources {
			queries = append(queries, auditcontract.AuditEventQuery{ObjectKey: resource.ObjectKey, RecordID: resource.RecordID, Limit: 500})
		}
		ids := []string{}
		seen := map[string]bool{}
		for _, query := range queries {
			for {
				events, err := repository.ListAuditEvents(ctx, workspace, query)
				if err != nil {
					return nil, err
				}
				for _, event := range events {
					if !seen[event.ID] {
						ids = append(ids, event.ID)
						seen[event.ID] = true
					}
				}
				if len(ids) > 10000 {
					return nil, fmt.Errorf("Runtime subject event limit exceeded")
				}
				if len(events) < query.Limit {
					break
				}
				next := auditcontract.EncodeCursor(events[len(events)-1])
				if next == query.Cursor {
					return nil, fmt.Errorf("Audit subject event cursor did not advance")
				}
				query.Cursor = next
			}
		}
		return ids, nil
	}
}
