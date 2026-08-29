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
	partymodel "github.com/domainry/domainry-party-sdk/contract"
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
	identityDirectoryNoop
	found           bool
	err             error
	departmentFound bool
	departmentErr   error
	workforce       []identitysdk.WorkforceEntry
	workforceErr    error
}

type identityReferenceOnlyProbe struct{ identityDirectoryNoop }

func (identityReferenceOnlyProbe) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}

func (identityReferenceOnlyProbe) FindDepartment(context.Context, identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	return identitysdk.Department{}, false, nil
}

type partyLookupProbe struct {
	value partymodel.Aggregate
	found bool
	err   error
}

func (p partyLookupProbe) Get(context.Context, string) (partymodel.Aggregate, bool, error) {
	return p.value, p.found, p.err
}

func (p identityLookupProbe) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{ID: "user-1"}, p.found, p.err
}

func (p identityLookupProbe) FindDepartment(context.Context, identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	return identitysdk.Department{ID: "department-1"}, p.departmentFound, p.departmentErr
}

func (p identityLookupProbe) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return p.workforce, p.workforceErr
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

func TestRelationValidatorValidatesTargetAndIdentityDirectory(t *testing.T) {
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

	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{found: true}})
	if err := validator.Validate(t.Context(), profile, map[string]any{"identity_user": "user-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("existing identity relation rejected: %v", err)
	}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{}})
	err = validator.Validate(t.Context(), profile, map[string]any{"identity_user": "missing"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "identity_user", "object": "identity_user"})

	transfer := definitionmodel.ObjectSchema{Key: "transfer", Fields: []definitionmodel.FieldSchema{{Key: "department", Type: "relation", Config: map[string]any{"target": "identity_department"}}}}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{departmentFound: true}})
	if err := validator.Validate(t.Context(), transfer, map[string]any{"department": "department-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("existing identity department relation rejected: %v", err)
	}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{}})
	err = validator.Validate(t.Context(), transfer, map[string]any{"department": "missing"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "department", "object": "identity_department"})

	workforceObject := definitionmodel.ObjectSchema{Key: "shift", Fields: []definitionmodel.FieldSchema{{Key: "worker", Type: "relation", Config: map[string]any{"target": "identity_workforce_profile"}}}}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{workforce: []identitysdk.WorkforceEntry{{WorkforceProfileID: "worker-1"}}}})
	if err := validator.Validate(t.Context(), workforceObject, map[string]any{"worker": "worker-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("existing workforce relation rejected: %v", err)
	}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{}})
	err = validator.Validate(t.Context(), workforceObject, map[string]any{"worker": "missing"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "worker", "object": "identity_workforce_profile"})
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
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{err: errors.New("directory unavailable")}})
	err = validator.Validate(t.Context(), profile, map[string]any{"identity_user": "user-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check identity user relation"})

	transfer := definitionmodel.ObjectSchema{Key: "transfer", Fields: []definitionmodel.FieldSchema{{Key: "department", Type: "relation", Config: map[string]any{"target": "identity_department"}}}}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{departmentErr: errors.New("directory unavailable")}})
	err = validator.Validate(t.Context(), transfer, map[string]any{"department": "department-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check identity department relation"})

	workforceObject := definitionmodel.ObjectSchema{Key: "shift", Fields: []definitionmodel.FieldSchema{{Key: "worker", Type: "relation", Config: map[string]any{"target": "identity_workforce_profile"}}}}
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{workforceErr: errors.New("directory unavailable")}})
	err = validator.Validate(t.Context(), workforceObject, map[string]any{"worker": "worker-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check identity workforce relation"})
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
	plannedContext := RecordWithPlannedRelations(ctx, records)
	planned := RecordPlannedRelations(plannedContext)
	if planned["customer"]["customer-1"].ID != "customer-1" {
		t.Fatalf("planned relations=%#v", planned)
	}
}

func TestRelationValidatorValidatesPartyFoundationTargets(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	for _, test := range []struct {
		target string
		kind   string
	}{
		{target: "party", kind: "person"},
		{target: "person", kind: "person"},
		{target: "organization", kind: "organization"},
	} {
		object := definitionmodel.ObjectSchema{Key: "reference", Fields: []definitionmodel.FieldSchema{{Key: "subject", Type: "relation", Config: map[string]any{"target": test.target}}}}
		validator := NewRecordRelationValidator(RecordRelationValidationDependencies{Party: partyLookupProbe{
			value: partymodel.Aggregate{Party: partymodel.Party{ID: "subject", Kind: test.kind}}, found: true,
		}})
		if err := validator.Validate(t.Context(), object, map[string]any{"subject": "subject"}, principal); err != nil {
			t.Fatalf("%s relation rejected: %v", test.target, err)
		}
	}
	object := definitionmodel.ObjectSchema{Key: "reference", Fields: []definitionmodel.FieldSchema{{Key: "subject", Type: "relation", Config: map[string]any{"target": "person"}}}}
	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{})
	err := validator.Validate(t.Context(), object, map[string]any{"subject": "subject"}, principal)
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check party relation"})
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Party: partyLookupProbe{value: partymodel.Aggregate{Party: partymodel.Party{Kind: "organization"}}, found: true}})
	err = validator.Validate(t.Context(), object, map[string]any{"subject": "subject"}, principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "subject", "object": "person"})
	validator = NewRecordRelationValidator(RecordRelationValidationDependencies{Party: partyLookupProbe{err: errors.New("party unavailable")}})
	err = validator.Validate(t.Context(), object, map[string]any{"subject": "subject"}, principal)
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check party relation"})
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

func TestRelationValidatorOrganizationAndWorkforceDependencyBoundaries(t *testing.T) {
	organizationReference := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{
		Key: "organization", Type: "relation", Config: map[string]any{"target": "identity_organization_unit"},
	}}}
	err := NewRecordRelationValidator(RecordRelationValidationDependencies{}).
		Validate(t.Context(), organizationReference, map[string]any{"organization": "unit-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check identity department relation"})

	workforceReference := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{
		Key: "worker", Type: "relation", Config: map[string]any{"target": "identity_workforce_profile"},
	}}}
	err = NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityReferenceOnlyProbe{}}).
		Validate(t.Context(), workforceReference, map[string]any{"worker": "worker-1"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "worker", "object": "identity_workforce_profile"})

	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{Identity: identityLookupProbe{
		workforce: []identitysdk.WorkforceEntry{
			{WorkforceProfileID: "other-worker"},
			{WorkforceProfileID: "worker-1"},
		},
	}})
	if err := validator.Validate(t.Context(), workforceReference, map[string]any{"worker": "worker-1"}, principalmodel.Principal{}); err != nil {
		t.Fatalf("workforce relation error=%v", err)
	}
}

func TestRelationValidatorPartyMissingPlannedScopeErrorAndDefaultAccess(t *testing.T) {
	partyReference := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{
		Key: "subject", Type: "relation", Config: map[string]any{"target": "party"},
	}}}
	err := NewRecordRelationValidator(RecordRelationValidationDependencies{Party: partyLookupProbe{}}).
		Validate(t.Context(), partyReference, map[string]any{"subject": "missing"}, principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.relation.record_missing", map[string]string{"field": "subject", "object": "party"})

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
