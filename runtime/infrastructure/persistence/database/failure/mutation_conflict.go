package failure

import "github.com/domainry/domainry-foundation/mutation"

func ConstraintError(err error, resource, identifier string, kind mutation.MutationConflictKind) error {
	return mutation.ConstraintError(err, resource, identifier, kind)
}

func TransactionError(err error, resource, identifier string) error {
	return mutation.TransactionError(err, resource, identifier)
}
