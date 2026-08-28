package integrationtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestConstraintCompilerEquivalentStrategiesAcrossDialects(t *testing.T) {
	key := time.Now().UnixNano()
	tests := []struct {
		name   string
		dsnEnv string
		config func(*testing.T, string) config.Config
	}{
		{name: "sqlite", config: func(t *testing.T, _ string) config.Config {
			return config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "constraints.db")}
		}},
		{name: "postgres", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN", config: func(t *testing.T, dsn string) config.Config {
			return realDialectPostgresConfig(t, dsn, fmt.Sprintf("runtime_constraint_%d", key))
		}},
		{name: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN", config: func(t *testing.T, dsn string) config.Config {
			return realDialectMySQLConfig(t, dsn, fmt.Sprintf("runtime_constraint_%d", key))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.dsnEnv))
			if test.dsnEnv != "" && dsn == "" {
				if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
					t.Fatalf("%s is required when RUNTIME_REQUIRE_REAL_DIALECTS=1", test.dsnEnv)
				}
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			runConstraintStrategyFixture(t, test.config(t, dsn))
		})
	}
}

func runConstraintStrategyFixture(t *testing.T, cfg config.Config) {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	parent := definitionmodel.ObjectSchema{Key: "constraint_parent", Fields: []definitionmodel.FieldSchema{
		{Key: "refund_limit", Type: "currency", Config: map[string]any{"precision": 12, "scale": 2}},
	}}
	unique := definitionmodel.ObjectSchema{Key: "constraint_unique", Fields: []definitionmodel.FieldSchema{
		{Key: "left_key", Type: "text"}, {Key: "right_key", Type: "text"},
	}, Validations: []definitionmodel.ValidationSchema{{Key: "unique_pair", Type: "composite_unique", Fields: []string{"left_key", "right_key"}}}}
	booking := definitionmodel.ObjectSchema{Key: "constraint_booking", Fields: []definitionmodel.FieldSchema{
		{Key: "class_id", Type: "relation"}, {Key: "member_id", Type: "relation"}, {Key: "status", Type: "select"},
	}, Validations: []definitionmodel.ValidationSchema{{
		Key: "one_active_booking", Type: "conditional_unique", Fields: []string{"class_id", "member_id"},
		Config: map[string]any{"condition_field": "status", "condition_values": []any{"booked", "waitlisted"}},
	}}}
	groupClass := definitionmodel.ObjectSchema{Key: "constraint_group_class", Fields: []definitionmodel.FieldSchema{
		{Key: "remaining_capacity", Type: "integer"},
		{Key: "remaining_waitlist_capacity", Type: "integer"},
	}}
	temporal := definitionmodel.ObjectSchema{Key: "constraint_slot", Fields: []definitionmodel.FieldSchema{
		{Key: "owner", Type: "text"}, {Key: "starts_at", Type: "datetime"}, {Key: "ends_at", Type: "datetime"},
	}, Validations: []definitionmodel.ValidationSchema{{Key: "owner_time", Type: "temporal_exclusion", Message: "business.owner_busy", Config: map[string]any{
		"start_field": "starts_at", "end_field": "ends_at", "scope_fields": []any{"owner"},
	}}}}
	aggregate := definitionmodel.ObjectSchema{Key: "constraint_refund", Fields: []definitionmodel.FieldSchema{
		{Key: "parent_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "constraint_parent"}},
		{Key: "amount", Type: "currency", Config: map[string]any{"precision": 12, "scale": 2}},
	}, Validations: []definitionmodel.ValidationSchema{{Key: "refund_limit", Type: "related_aggregate_invariant", Message: "business.refund_limit", Config: map[string]any{
		"relation_field": "parent_id", "aggregate": "sum", "value_field": "amount", "limit_field": "refund_limit", "operator": "lte",
	}}}}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{parent, unique, booking, groupClass, temporal, aggregate}}
	metadata := metadatapersistence.NewMetadataStore(store)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "verify cross-dialect constraint compiler")
	if err := metadata.SyncManifest(t.Context(), scope, manifest); err != nil {
		t.Fatal(err)
	}
	records := recordpersistence.NewRecordStore(store)
	stamp := "2026-07-21T00:00:00Z"
	create := func(workspace string, object definitionmodel.ObjectSchema, id string, data map[string]any) error {
		return records.CommitRecordMutation(t.Context(), workspace, transactionmodel.RecordMutationCommit{
			Operation: "create", Object: object,
			Record: recordmodel.Record{ID: id, CreatedAt: stamp, UpdatedAt: stamp, Data: data},
		})
	}
	if err := create("workspace-a", parent, "parent-a", map[string]any{"refund_limit": "100.00"}); err != nil {
		t.Fatal(err)
	}
	if err := create("workspace-a", unique, "unique-a", map[string]any{"left_key": "left", "right_key": "right"}); err != nil {
		t.Fatal(err)
	}
	if err := create("workspace-a", unique, "unique-conflict", map[string]any{"left_key": "left", "right_key": "right"}); err == nil {
		t.Fatal("composite unique accepted a duplicate tuple")
	}
	if err := create("workspace-b", unique, "unique-other-workspace", map[string]any{"left_key": "left", "right_key": "right"}); err != nil {
		t.Fatalf("workspace-scoped composite unique rejected independent tuple: %v", err)
	}
	if err := create("workspace-a", booking, "booking-a", map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "booked"}); err != nil {
		t.Fatal(err)
	}
	if err := create("workspace-a", booking, "booking-active-conflict", map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "waitlisted"}); !mutation.IsMutationConflict(err, mutation.MutationConflictUnique) {
		t.Fatalf("conditional unique did not reject a second active booking: %v", err)
	}
	for _, id := range []string{"booking-cancelled-a", "booking-cancelled-b"} {
		if err := create("workspace-a", booking, id, map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "cancelled"}); err != nil {
			t.Fatalf("conditional unique rejected inactive booking %s: %v", id, err)
		}
	}
	if err := create("workspace-b", booking, "booking-other-workspace", map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "waitlisted"}); err != nil {
		t.Fatalf("workspace-scoped conditional unique rejected independent active booking: %v", err)
	}
	if err := create("workspace-a", groupClass, "class-1", map[string]any{
		"remaining_capacity":          int64(1),
		"remaining_waitlist_capacity": int64(1),
	}); err != nil {
		t.Fatal(err)
	}
	reserve := transactionmodel.RecordMutationCommit{
		Operation: "update", Object: groupClass,
		Record: recordmodel.Record{
			ID: "class-1", CreatedAt: stamp, UpdatedAt: "2026-07-21T00:00:01Z",
			Data: map[string]any{
				"remaining_capacity":          int64(0),
				"remaining_waitlist_capacity": int64(1),
			},
		},
		Predicates: []transactionmodel.MutationPredicate{{
			Field: "remaining_capacity", Operator: "gte", Value: int64(1), ErrorCode: "gym.class_capacity_full",
		}},
	}
	if err := records.CommitRecordMutation(t.Context(), "workspace-a", reserve); err != nil {
		t.Fatalf("first atomic capacity reservation failed: %v", err)
	}
	reserve.Record.UpdatedAt = "2026-07-21T00:00:02Z"
	assertConstraintBusinessConflict(t, records.CommitRecordMutation(t.Context(), "workspace-a", reserve), "gym.class_capacity_full")
	classRecord, found, err := records.GetRecord(t.Context(), "workspace-a", groupClass, "class-1")
	if err != nil || !found ||
		classRecord.Data["remaining_capacity"] != int64(0) ||
		classRecord.Data["remaining_waitlist_capacity"] != int64(1) {
		t.Fatalf("atomic capacity result=%#v found=%v err=%v", classRecord, found, err)
	}
	waitlist := transactionmodel.RecordMutationCommit{
		Operation: "update", Object: groupClass,
		Record: recordmodel.Record{
			ID: "class-1", CreatedAt: stamp, UpdatedAt: "2026-07-21T00:00:03Z",
			Data: map[string]any{
				"remaining_capacity":          int64(0),
				"remaining_waitlist_capacity": int64(0),
			},
		},
		Predicates: []transactionmodel.MutationPredicate{{
			Field: "remaining_waitlist_capacity", Operator: "gte", Value: int64(1), ErrorCode: "gym.class_waitlist_full",
		}},
	}
	if err := records.CommitRecordMutation(t.Context(), "workspace-a", waitlist); err != nil {
		t.Fatalf("atomic waitlist reservation failed: %v", err)
	}
	classRecord, found, err = records.GetRecord(t.Context(), "workspace-a", groupClass, "class-1")
	if err != nil || !found ||
		classRecord.Data["remaining_capacity"] != int64(0) ||
		classRecord.Data["remaining_waitlist_capacity"] != int64(0) {
		t.Fatalf("atomic waitlist result=%#v found=%v err=%v", classRecord, found, err)
	}
	waitlist.Record.UpdatedAt = "2026-07-21T00:00:04Z"
	assertConstraintBusinessConflict(t, records.CommitRecordMutation(t.Context(), "workspace-a", waitlist), "gym.class_waitlist_full")
	classRecord, found, err = records.GetRecord(t.Context(), "workspace-a", groupClass, "class-1")
	if err != nil || !found ||
		classRecord.Data["remaining_capacity"] != int64(0) ||
		classRecord.Data["remaining_waitlist_capacity"] != int64(0) {
		t.Fatalf("full waitlist changed capacity=%#v found=%v err=%v", classRecord, found, err)
	}
	if err := create("workspace-a", temporal, "slot-a", map[string]any{"owner": "owner-a", "starts_at": "2026-07-21T10:00:00Z", "ends_at": "2026-07-21T11:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	assertConstraintBusinessConflict(t, create("workspace-a", temporal, "slot-conflict", map[string]any{"owner": "owner-a", "starts_at": "2026-07-21T10:30:00Z", "ends_at": "2026-07-21T11:30:00Z"}), "business.owner_busy")
	if err := create("workspace-a", aggregate, "refund-a", map[string]any{"parent_id": "parent-a", "amount": "100.00"}); err != nil {
		t.Fatal(err)
	}
	assertConstraintBusinessConflict(t, create("workspace-a", aggregate, "refund-conflict", map[string]any{"parent_id": "parent-a", "amount": "0.01"}), "business.refund_limit")
}

func assertConstraintBusinessConflict(t *testing.T, err error, code string) {
	t.Helper()
	var conflict *mutation.PolicyConflictError
	if !errors.As(err, &conflict) || conflict.Code != code {
		t.Fatalf("business conflict code=%s conflict=%#v err=%v", code, conflict, err)
	}
}
