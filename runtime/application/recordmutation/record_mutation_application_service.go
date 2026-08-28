package recordmutation

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type RecordMutationOperation string

const (
	RecordMutationCreate     RecordMutationOperation = "create"
	RecordMutationUpdate     RecordMutationOperation = "update"
	RecordMutationDelete     RecordMutationOperation = "delete"
	RecordMutationRestore    RecordMutationOperation = "restore"
	RecordMutationTransition RecordMutationOperation = "transition"
)

type RecordMutationRequest struct {
	Operation RecordMutationOperation
	ObjectKey string
	RecordID  string
	Data      map[string]any
	Principal principalmodel.Principal
}

type RecordMutationOperations interface {
	CreateRecord(context.Context, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error)
	UpdateRecord(context.Context, string, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error)
	DeleteRecord(context.Context, string, string, principalmodel.Principal) error
	RestoreRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
}

type RecordMutationApplicationService struct {
	operations RecordMutationOperations
}

func NewRecordMutationApplicationService(operations RecordMutationOperations) *RecordMutationApplicationService {
	return &RecordMutationApplicationService{operations: operations}
}

// Dispatch is the canonical entry point for user-visible writes initiated by
// cross-domain orchestrators. Internal evidence writers use
// InternalMutationService instead.
func (s *RecordMutationApplicationService) Dispatch(ctx context.Context, request RecordMutationRequest) (recordmodel.Record, error) {
	request.ObjectKey = strings.TrimSpace(request.ObjectKey)
	request.RecordID = strings.TrimSpace(request.RecordID)
	switch request.Operation {
	case RecordMutationCreate:
		return s.operations.CreateRecord(ctx, request.ObjectKey, request.Data, request.Principal)
	case RecordMutationUpdate, RecordMutationTransition:
		return s.operations.UpdateRecord(ctx, request.ObjectKey, request.RecordID, request.Data, request.Principal)
	case RecordMutationDelete:
		return recordmodel.Record{}, s.operations.DeleteRecord(ctx, request.ObjectKey, request.RecordID, request.Principal)
	case RecordMutationRestore:
		return s.operations.RestoreRecord(ctx, request.ObjectKey, request.RecordID, request.Principal)
	default:
		return recordmodel.Record{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.record.mutation_operation_invalid", Params: map[string]string{"operation": string(request.Operation)}}
	}
}
