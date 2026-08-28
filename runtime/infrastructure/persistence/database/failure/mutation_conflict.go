package failure

import (
	"errors"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

func ConstraintError(err error, resource, identifier string, kind mutation.MutationConflictKind) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate entry") || strings.Contains(message, "duplicate key") || strings.Contains(message, "primary key constraint") || strings.Contains(message, "sqlstate 23505") || strings.Contains(message, "error 1062") || strings.Contains(message, "constraint failed (1555)") || strings.Contains(message, "constraint failed (2067)") {
		return mutation.MutationConflict(resource, identifier, kind, err)
	}
	return err
}

func TransactionError(err error, resource, identifier string) error {
	if err == nil {
		return nil
	}
	var conflict *mutation.MutationConflictError
	if errors.As(err, &conflict) {
		return err
	}
	var transient *mutation.TransactionTransientError
	if errors.As(err, &transient) {
		return err
	}
	message := strings.ToLower(err.Error())
	if containsAny(message, "error 1213", "deadlock found", "sqlstate 40p01", "deadlock detected") {
		return mutation.TransactionTransient(resource, identifier, mutation.TransactionTransientDeadlock, err)
	}
	if containsAny(message, "sqlstate 40001", "serialization failure", "could not serialize access") {
		return mutation.TransactionTransient(resource, identifier, mutation.TransactionTransientSerializationFailure, err)
	}
	if containsAny(message, "database is locked", "database table is locked", "database is busy", "sqlite_busy", "error 1205", "lock wait timeout exceeded", "sqlstate 55p03", "lock timeout") {
		return mutation.TransactionTransient(resource, identifier, mutation.TransactionTransientLockTimeout, err)
	}
	return err
}

func containsAny(message string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
