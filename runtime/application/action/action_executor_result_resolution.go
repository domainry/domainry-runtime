package action

import (
	"context"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// resolveInvocationRecord supports Actions invoked with a provider-visible
// business key (for example merchant_order_no). A unique Action-object mutation
// or observation is the unambiguous canonical Runtime result; only otherwise do
// we reload the invocation ID as a primary key.
func (e *businessActionExecution) resolveInvocationRecord(ctx context.Context, objectKey, recordID string) (recordmodel.Record, error) {
	if record, found := e.mutatedRecord(objectKey, recordID); found {
		return record, nil
	}
	if record, found := e.singleMutatedRecord(objectKey); found {
		return record, nil
	}
	if record, found := e.singleObservedRecord(objectKey); found {
		return record, nil
	}
	if e.dependencies.GetRecord == nil {
		return recordmodel.Record{}, missingExecutorPort("get_record")
	}
	return e.dependencies.GetRecord(e.unitOfWork.executionContext(ctx), objectKey, recordID, e.invocation.Principal)
}

func (e *businessActionExecution) singleMutatedRecord(objectKey string) (recordmodel.Record, bool) {
	return singleActionObjectRecord(e.mutatedRecords, objectKey)
}

func (e *businessActionExecution) singleObservedRecord(objectKey string) (recordmodel.Record, bool) {
	return singleActionObjectRecord(e.observedRecords, objectKey)
}

func singleActionObjectRecord(records map[string]recordmodel.Record, objectKey string) (recordmodel.Record, bool) {
	prefix := strings.TrimSpace(objectKey) + "\x00"
	var resolved recordmodel.Record
	found := false
	for key, record := range records {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if found {
			return recordmodel.Record{}, false
		}
		resolved, found = record, true
	}
	return resolved, found
}
