package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type relationRepositoryProbe struct {
	recordrepository.RecordRepository
	record recordmodel.Record
	found  bool
	err    error
}

func (r *relationRepositoryProbe) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.err
}

type identityLookupProbe struct {
	identityProjectionNoop
	found                 bool
	err                   error
	organizationUnitFound bool
	organizationUnitErr   error
}

func (p identityLookupProbe) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{ID: "user-1"}, p.found, p.err
}

func (p identityLookupProbe) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{ID: "organization-unit-1"}, p.organizationUnitFound, p.organizationUnitErr
}

func TestRelationValidatorValidatesRecordExistenceAndScope(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer"}
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Config: map[string]any{"target": "customer"}}}}
	repository := &relationRepositoryProbe{record: recordmodel.Record{ID: "customer-1"}, found: true}
	allowed := true
	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			return customer, key == customer.Key
		},
		CanAccessRecord: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return allowed },
	})

	if err := validator.Validate(t.Context(), order, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("valid relation rejected: %v", err)
	}
	allowed = false
	err := validator.Validate(t.Context(), order, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.record.outside_scope", nil)

	repository.found = false
	allowed = true
	err = validator.Validate(t.Context(), order, map[string]any{"customer_id": "missing"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "customer_id", "object": "customer"})
}

func TestRelationValidatorPrefersPersistedScopeEvaluatorAndPropagatesErrors(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer"}
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{
		Key: "customer_id", Type: "relation", Config: map[string]any{"target": "customer"},
	}}}
	repository := &relationRepositoryProbe{record: recordmodel.Record{ID: "customer-1"}, found: true}
	type scopeContextKey struct{}
	ctx := context.WithValue(t.Context(), scopeContextKey{}, "transaction")
	allowed := true
	var scopeErr error
	legacyCalls := 0
	persistedCalls := 0
	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			return customer, key == customer.Key
		},
		CanAccessRecord: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
			legacyCalls++
			return false
		},
		CanAccessPersistedRecord: func(
			callCtx context.Context,
			_ principalmodel.Principal,
			object definitionmodel.ObjectSchema,
			record recordmodel.Record,
		) (bool, error) {
			persistedCalls++
			if callCtx.Value(scopeContextKey{}) != "transaction" {
				t.Fatal("persisted scope evaluator did not receive the validation context")
			}
			if object.Key != "customer" || record.ID != "customer-1" {
				t.Fatalf("persisted scope input object=%s record=%s", object.Key, record.ID)
			}
			return allowed, scopeErr
		},
	})

	if err := validator.Validate(ctx, order, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("persisted scope allowed relation was rejected: %v", err)
	}
	allowed = false
	err := validator.Validate(ctx, order, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.record.outside_scope", nil)

	scopeErr = errors.New("scope evaluator unavailable")
	err = validator.Validate(ctx, order, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{})
	if !errors.Is(err, scopeErr) {
		t.Fatalf("scope evaluator error=%v, want %v", err, scopeErr)
	}
	if legacyCalls != 0 || persistedCalls != 3 {
		t.Fatalf("legacyCalls=%d persistedCalls=%d", legacyCalls, persistedCalls)
	}
}

func TestRelationValidatorValidatesTargetAndIdentityProjection(t *testing.T) {
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}}}}
	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{
		Repository: &relationRepositoryProbe{},
		Object: func(context.Context, string) (definitionmodel.ObjectSchema, bool) {
			return definitionmodel.ObjectSchema{}, false
		},
		CanAccessRecord: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	err := validator.Validate(t.Context(), order, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.target_unavailable", map[string]string{"field": "customer_id", "object": "customer"})

	profile := definitionmodel.ObjectSchema{Key: "employee_profile", Fields: []definitionmodel.FieldSchema{{Key: "identity_user", Type: "relation", Config: map[string]any{"object_key": "identity_user"}}}}
	err = validator.Validate(t.Context(), profile, map[string]any{"identity_user": "user-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check identity user relation"})
	plannedIdentityContext := RecordWithPlannedIdentityUsers(t.Context(), "user-1")
	if err := validator.Validate(plannedIdentityContext, profile, map[string]any{"identity_user": "user-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("same-UoW planned Identity user relation rejected: %v", err)
	}
	err = validator.Validate(plannedIdentityContext, profile, map[string]any{"identity_user": "sibling-user"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check identity user relation"})

	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{found: true}})
	if err := validator.Validate(t.Context(), profile, map[string]any{"identity_user": "user-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("existing identity relation rejected: %v", err)
	}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{}})
	err = validator.Validate(t.Context(), profile, map[string]any{"identity_user": "missing"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "identity_user", "object": "identity_user"})

	transfer := definitionmodel.ObjectSchema{Key: "transfer", Fields: []definitionmodel.FieldSchema{{Key: "organization_unit", Type: "relation", Config: map[string]any{"target": "identity_organization_unit"}}}}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{organizationUnitFound: true}})
	if err := validator.Validate(t.Context(), transfer, map[string]any{"organization_unit": "organization-unit-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("existing identity organization unit relation rejected: %v", err)
	}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{}})
	err = validator.Validate(t.Context(), transfer, map[string]any{"organization_unit": "missing"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "organization_unit", "object": "identity_organization_unit"})
}

func TestRelationValidatorWrapsRepositoryAndIdentityErrors(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Config: map[string]any{"target": "customer"}}}}
	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{
		Repository: &relationRepositoryProbe{err: errors.New("store unavailable")},
		Object: func(context.Context, string) (definitionmodel.ObjectSchema, bool) {
			return definitionmodel.ObjectSchema{Key: "customer"}, true
		},
		CanAccessRecord: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	err := validator.Validate(t.Context(), object, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check relation field"})

	profile := definitionmodel.ObjectSchema{Key: "employee_profile", Fields: []definitionmodel.FieldSchema{{Key: "identity_user", Type: "relation", Config: map[string]any{"target": "identity_user"}}}}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{err: errors.New("projection unavailable")}})
	err = validator.Validate(t.Context(), profile, map[string]any{"identity_user": "user-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check identity user relation"})

	transfer := definitionmodel.ObjectSchema{Key: "transfer", Fields: []definitionmodel.FieldSchema{{Key: "organization_unit", Type: "relation", Config: map[string]any{"target": "identity_organization_unit"}}}}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{organizationUnitErr: errors.New("projection unavailable")}})
	err = validator.Validate(t.Context(), transfer, map[string]any{"organization_unit": "organization-unit-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check identity organization unit relation"})
}

func TestPlannedRelationContextNilAndEmptyEdges(t *testing.T) {
	records := map[string]map[string]recordmodel.Record{"customer": {"customer-1": {ID: "customer-1"}}}
	if got := RecordWithPlannedRelations(nil, records); got != nil {
		t.Fatalf("nil context became %#v", got)
	}
	ctx := t.Context()
	if got := RecordWithPlannedRelations(ctx, nil); got != ctx {
		t.Fatal("empty records replaced context")
	}
	if got := RecordPlannedRelations(ctx); got != nil {
		t.Fatalf("empty planned relations=%#v", got)
	}
	if got := RecordPlannedRelations(nil); got != nil {
		t.Fatalf("nil planned relations=%#v", got)
	}
	if got := RecordWithPlannedIdentityUsers(nil, "user-1"); got != nil {
		t.Fatalf("nil planned Identity context became %#v", got)
	}
	if got := RecordWithPlannedIdentityUsers(ctx, " "); got != ctx {
		t.Fatal("empty planned Identity user replaced context")
	}
	plannedContext := RecordWithPlannedRelations(ctx, records)
	planned := RecordPlannedRelations(plannedContext)
	if planned["customer"]["customer-1"].ID != "customer-1" {
		t.Fatalf("planned relations=%#v", planned)
	}
}

func TestRelationValidatorAcceptsRepositoryUpdate(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Config: map[string]any{"target": "customer"}}}}
	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{
		Repository: &relationRepositoryProbe{found: true},
		Object: func(context.Context, string) (definitionmodel.ObjectSchema, bool) {
			return definitionmodel.ObjectSchema{Key: "customer"}, true
		},
		CanAccessRecord: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	if err := validator.Validate(t.Context(), object, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("constructor repository was not used: %v", err)
	}
}

func TestRelationValidatorAcceptsOnlyAlreadyPlannedAccessibleBatchRelations(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer"}
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}}}}
	repository := &relationRepositoryProbe{}
	allowed := true
	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			return customer, key == customer.Key
		},
		CanAccessRecord: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return allowed },
	})
	planned := map[string]map[string]recordmodel.Record{"customer": {"customer-planned": {ID: "customer-planned", Data: map[string]any{"status": "active"}}}}
	ctx := RecordWithPlannedRelations(t.Context(), planned)
	planned["customer"]["customer-planned"].Data["status"] = "mutated-after-context"
	if err := validator.Validate(ctx, order, map[string]any{"customer_id": "customer-planned"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("planned relation rejected: %v", err)
	}
	allowed = false
	err := validator.Validate(ctx, order, map[string]any{"customer_id": "customer-planned"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.record.outside_scope", nil)
	allowed = true
	err = validator.Validate(ctx, order, map[string]any{"customer_id": "future-record"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "customer_id", "object": "customer"})
}

func TestPlannedRelationContextHandlesNilAndEmptyInputs(t *testing.T) {
	if ctx := RecordWithPlannedRelations(nil, map[string]map[string]recordmodel.Record{
		"customer": {"customer-1": {ID: "customer-1"}},
	}); ctx != nil {
		t.Fatalf("nil context changed to %#v", ctx)
	}
	ctx := t.Context()
	if got := RecordWithPlannedRelations(ctx, nil); got != ctx {
		t.Fatal("empty planned relation set changed context")
	}
	if record, found := recordPlannedRelation(nil, "customer", "customer-1"); found || record.ID != "" {
		t.Fatalf("nil lookup record=%#v found=%v", record, found)
	}
}

func TestRelationValidatorOrganizationDependencyBoundaries(t *testing.T) {
	organizationReference := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{
		Key: "organization", Type: "relation", Config: map[string]any{"target": "identity_organization_unit"},
	}}}
	err := NewRecordRelationValidator(RecordRelationValidationDependencies{}).
		Validate(t.Context(), organizationReference, map[string]any{"organization": "unit-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check identity organization unit relation"})
}

func TestRelationValidatorPlannedScopeErrorAndDefaultAccess(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer"}
	order := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{
		Key: "customer_id", Type: "relation", Config: map[string]any{"target": "customer"},
	}}}
	scopeErr := errors.New("planned scope unavailable")
	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{
		Repository: &relationRepositoryProbe{},
		Object: func(context.Context, string) (definitionmodel.ObjectSchema, bool) {
			return customer, true
		},
		CanAccessPersistedRecord: func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) (bool, error) {
			return false, scopeErr
		},
	})
	ctx := RecordWithPlannedRelations(t.Context(), map[string]map[string]recordmodel.Record{
		"customer": {"customer-1": {ID: "customer-1"}},
	})
	if err := validator.Validate(ctx, order, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{}); !errors.Is(err, scopeErr) {
		t.Fatalf("planned scope error=%v", err)
	}

	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{
		Repository: &relationRepositoryProbe{record: recordmodel.Record{ID: "customer-1"}, found: true},
		Object: func(context.Context, string) (definitionmodel.ObjectSchema, bool) {
			return customer, true
		},
	})
	if err := validator.Validate(t.Context(), order, map[string]any{"customer_id": "customer-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("default access error=%v", err)
	}
}
