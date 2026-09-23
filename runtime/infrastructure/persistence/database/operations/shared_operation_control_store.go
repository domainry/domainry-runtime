package operations

import (
	"context"

	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// SharedOperationControlStore exposes Runtime's canonical
// _operation_controls registry to embedded owners without leaking Runtime
// domain types across the module boundary.
type SharedOperationControlStore struct{ store OperationsStore }

func NewSharedOperationControlStore(store *database.RuntimeStore) SharedOperationControlStore {
	return SharedOperationControlStore{store: NewOperationsStore(store)}
}

func (s SharedOperationControlStore) GetOperationControl(ctx context.Context, purpose, kind, owner string) (notificationmodulehost.OperationControl, bool, error) {
	value, found, err := s.store.GetOperationsControl(ctx, purpose, operationsmodel.OperationsControlKind(kind), owner)
	if err != nil || !found {
		return notificationmodulehost.OperationControl{}, found, err
	}
	return notificationmodulehost.OperationControl{
		SystemPurpose: value.SystemPurpose,
		Kind:          string(value.Kind),
		Owner:         value.Owner,
		State:         string(value.State),
		Reason:        value.Reason,
		Reference:     value.Reference,
		UpdatedBy:     value.UpdatedBy,
		Revision:      value.Revision,
		UpdatedAt:     value.UpdatedAt,
	}, true, nil
}

func (s SharedOperationControlStore) PutOperationControl(ctx context.Context, value notificationmodulehost.OperationControl, expectedRevision int64) (bool, error) {
	if err := value.Validate(); err != nil {
		return false, err
	}
	return s.store.PutOperationsControl(ctx, operationsmodel.OperationsControl{
		SystemPurpose: value.SystemPurpose,
		Kind:          operationsmodel.OperationsControlKind(value.Kind),
		Owner:         value.Owner,
		State:         operationsmodel.OperationsControlState(value.State),
		Reason:        value.Reason,
		Reference:     value.Reference,
		UpdatedBy:     value.UpdatedBy,
		Revision:      value.Revision,
		UpdatedAt:     value.UpdatedAt,
	}, expectedRevision)
}

var _ notificationmodulehost.OperationControlStore = SharedOperationControlStore{}
