package operations

import (
	"context"

	sharedoperation "github.com/domainry/domainry-foundation/operation"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.OperationsControlRepository = OperationsStore{}

func (s OperationsStore) GetOperationsControl(ctx context.Context, purpose string, kind operationsmodel.OperationsControlKind, owner string) (operationsmodel.OperationsControl, bool, error) {
	ledger, err := s.ledger()
	if err != nil {
		return operationsmodel.OperationsControl{}, false, err
	}
	value, found, err := ledger.GetControl(ctx, purpose, string(kind), owner)
	if err != nil || !found {
		return operationsmodel.OperationsControl{}, found, err
	}
	return operationsControl(value), true, nil
}

func (s OperationsStore) ListOperationsControls(ctx context.Context, purpose string, kind operationsmodel.OperationsControlKind, limit int) ([]operationsmodel.OperationsControl, error) {
	ledger, err := s.ledger()
	if err != nil {
		return nil, err
	}
	values, err := ledger.ListControls(ctx, purpose, string(kind), limit)
	if err != nil {
		return nil, err
	}
	result := make([]operationsmodel.OperationsControl, 0, len(values))
	for _, value := range values {
		result = append(result, operationsControl(value))
	}
	return result, nil
}

func (s OperationsStore) PutOperationsControl(ctx context.Context, control operationsmodel.OperationsControl, expectedRevision int64) (bool, error) {
	ledger, err := s.ledger()
	if err != nil {
		return false, err
	}
	return ledger.PutControl(ctx, sharedoperation.Control{
		SystemPurpose: control.SystemPurpose, Kind: string(control.Kind), Owner: control.Owner, State: string(control.State),
		Reason: control.Reason, Reference: control.Reference, UpdatedBy: control.UpdatedBy, Revision: control.Revision, UpdatedAt: control.UpdatedAt,
	}, expectedRevision)
}

func operationsControl(value sharedoperation.Control) operationsmodel.OperationsControl {
	return operationsmodel.OperationsControl{
		SystemPurpose: value.SystemPurpose, Kind: operationsmodel.OperationsControlKind(value.Kind), Owner: value.Owner,
		State: operationsmodel.OperationsControlState(value.State), Reason: value.Reason, Reference: value.Reference,
		UpdatedBy: value.UpdatedBy, Revision: value.Revision, UpdatedAt: value.UpdatedAt,
	}
}
