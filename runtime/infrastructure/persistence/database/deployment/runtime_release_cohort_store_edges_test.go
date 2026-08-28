package deployment

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

var errReleaseSQL = errors.New("release SQL failure")

func releaseIdentity(hash string) deploymentmodel.RuntimeReleaseIdentity {
	return deploymentmodel.RuntimeReleaseIdentity{ContractVersion: "v1", CombinationSHA256: hash}
}

func releaseClaim(now time.Time) deploymentmodel.RuntimeReleaseCohortClaim {
	return deploymentmodel.RuntimeReleaseCohortClaim{
		InstanceID: "instance", Identity: releaseIdentity("hash"), Now: now, LeaseDuration: time.Minute,
	}
}

func withReleaseTx(t *testing.T, store RuntimeReleaseCohortStore, call func(*sql.Tx) error) error {
	t.Helper()
	tx, err := store.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return call(tx)
}

func TestRuntimeReleaseClaimUnavailableAndRetryEdges(t *testing.T) {
	if _, err := NewRuntimeReleaseCohortStore(nil).ClaimRuntimeRelease(t.Context(), deploymentmodel.RuntimeReleaseCohortClaim{}); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) {
		t.Fatalf("unavailable error=%v", err)
	}
	if _, err := NewRuntimeReleaseCohortStore(releaseSQLStore{}).ClaimRuntimeRelease(t.Context(), deploymentmodel.RuntimeReleaseCohortClaim{}); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) {
		t.Fatalf("nil database error=%v", err)
	}
	if runtimeReleaseCoordinationRetryable(nil) || runtimeReleaseCoordinationRetryable(errors.New("plain")) {
		t.Fatal("plain errors marked retryable")
	}
	for _, message := range []string{
		"unique", "duplicate", "database is locked", "database table is locked", "SQLITE_BUSY",
		"deadlock", "SQLSTATE 40001", "could not serialize", "lock wait timeout",
	} {
		if !runtimeReleaseCoordinationRetryable(errors.New(message)) {
			t.Fatalf("retry marker %q missed", message)
		}
	}
	store, closeDB := scriptedReleaseStore(&releaseSQLState{beginErr: errReleaseSQL})
	if _, err := store.ClaimRuntimeRelease(t.Context(), releaseClaim(time.Now())); err != errReleaseSQL {
		t.Fatalf("nonretryable error=%v", err)
	}
	closeDB()

	retryStore, closeRetry := scriptedReleaseStore(&releaseSQLState{beginErr: errors.New("database is locked")})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(time.Millisecond)
		cancel()
	}()
	if _, err := retryStore.ClaimRuntimeRelease(ctx, releaseClaim(time.Now())); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retry error=%v", err)
	}
	closeRetry()
	exhaustedStore, closeExhausted := scriptedReleaseStore(&releaseSQLState{beginErr: errors.New("database is locked")})
	if _, err := exhaustedStore.ClaimRuntimeRelease(t.Context(), releaseClaim(time.Now())); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) {
		t.Fatalf("retry exhaustion error=%v", err)
	}
	closeExhausted()
}

func TestRuntimeReleaseLockCohortStages(t *testing.T) {
	tests := []struct {
		name  string
		state *releaseSQLState
		err   bool
		found bool
	}{
		{"update", &releaseSQLState{execSteps: []releaseSQLExecStep{{err: errReleaseSQL}}}, true, false},
		{"rows", &releaseSQLState{execSteps: []releaseSQLExecStep{{rowsErr: errReleaseSQL}}}, true, false},
		{"insert", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 0}, {err: errReleaseSQL}}}, true, false},
		{"select", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{err: errReleaseSQL}}}, true, false},
		{"empty", &releaseSQLState{
			execSteps:  []releaseSQLExecStep{{rows: 0}, {rows: 1}},
			querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3), rows: [][]driver.Value{{"", "", int64(0)}}}},
		}, false, false},
		{"active", &releaseSQLState{
			execSteps:  []releaseSQLExecStep{{rows: 1}},
			querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3), rows: [][]driver.Value{{"hash", "{}", int64(2)}}}},
		}, false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedReleaseStore(test.state)
			defer closeDB()
			var found bool
			err := withReleaseTx(t, store, func(tx *sql.Tx) error {
				_, foundValue, callErr := store.lockCohort(t.Context(), tx)
				found = foundValue
				return callErr
			})
			if test.err != (err != nil) || found != test.found {
				t.Fatalf("found=%v err=%v", found, err)
			}
		})
	}
}

func TestRuntimeReleaseLiveInstancesStages(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name  string
		state *releaseSQLState
		err   bool
		live  int
	}{
		{"query", &releaseSQLState{querySteps: []releaseSQLQueryStep{{err: errReleaseSQL}}}, true, 0},
		{"scan", &releaseSQLState{querySteps: []releaseSQLQueryStep{{columns: []string{"only"}, rows: [][]driver.Value{{"value"}}}}}, true, 0},
		{"expiry", &releaseSQLState{querySteps: []releaseSQLQueryStep{{columns: releaseColumns(4), rows: [][]driver.Value{{"id", "hash", int64(1), "bad"}}}}}, true, 0},
		{"iterate", &releaseSQLState{querySteps: []releaseSQLQueryStep{{columns: releaseColumns(4), nextErr: errReleaseSQL}}}, true, 0},
		{"delete", &releaseSQLState{
			querySteps: []releaseSQLQueryStep{{columns: releaseColumns(4), rows: [][]driver.Value{{"expired", "hash", int64(1), now.Add(-time.Minute).Format(time.RFC3339Nano)}}}},
			execSteps:  []releaseSQLExecStep{{err: errReleaseSQL}},
		}, true, 0},
		{"mixed", &releaseSQLState{
			querySteps: []releaseSQLQueryStep{{columns: releaseColumns(4), rows: [][]driver.Value{
				{"expired", "hash", int64(1), now.Add(-time.Minute).Format(time.RFC3339Nano)},
				{"live", "hash", int64(1), now.Add(time.Minute).Format(time.RFC3339Nano)},
			}}},
			execSteps: []releaseSQLExecStep{{rows: 1}},
		}, false, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedReleaseStore(test.state)
			defer closeDB()
			var live []runtimeReleaseInstanceRow
			err := withReleaseTx(t, store, func(tx *sql.Tx) error {
				var callErr error
				live, callErr = store.liveInstances(t.Context(), tx, now)
				return callErr
			})
			if test.err != (err != nil) || len(live) != test.live {
				t.Fatalf("live=%#v err=%v", live, err)
			}
		})
	}
}

func TestRuntimeReleaseReplaceAndLeaseWriteStages(t *testing.T) {
	now := time.Now().UTC()
	for _, state := range []*releaseSQLState{
		{execSteps: []releaseSQLExecStep{{err: errReleaseSQL}}},
		{execSteps: []releaseSQLExecStep{{rowsErr: errReleaseSQL}}},
		{execSteps: []releaseSQLExecStep{{rows: 0}}},
	} {
		store, closeDB := scriptedReleaseStore(state)
		err := withReleaseTx(t, store, func(tx *sql.Tx) error {
			return store.replaceCohort(t.Context(), tx, 1, "hash", "{}", now)
		})
		closeDB()
		if err == nil {
			t.Fatal("replace failure ignored")
		}
	}
	store, closeDB := scriptedReleaseStore(&releaseSQLState{})
	if err := withReleaseTx(t, store, func(tx *sql.Tx) error {
		return store.replaceCohort(t.Context(), tx, 1, "hash", "{}", now)
	}); err != nil {
		t.Fatalf("replace success=%v", err)
	}
	closeDB()

	lease := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "instance", CombinationSHA256: "hash", Generation: 1, ExpiresAt: now.Add(time.Minute)}
	for _, state := range []*releaseSQLState{
		{execSteps: []releaseSQLExecStep{{err: errReleaseSQL}}},
		{execSteps: []releaseSQLExecStep{{rowsErr: errReleaseSQL}}},
		{execSteps: []releaseSQLExecStep{{rows: 0}, {err: errReleaseSQL}}},
	} {
		store, closeDB = scriptedReleaseStore(state)
		err := withReleaseTx(t, store, func(tx *sql.Tx) error {
			return store.writeInstanceLease(t.Context(), tx, lease, now)
		})
		closeDB()
		if err == nil {
			t.Fatal("lease write failure ignored")
		}
	}
	for _, state := range []*releaseSQLState{
		{execSteps: []releaseSQLExecStep{{rows: 1}}},
		{execSteps: []releaseSQLExecStep{{rows: 0}, {rows: 1}}},
	} {
		store, closeDB = scriptedReleaseStore(state)
		if err := withReleaseTx(t, store, func(tx *sql.Tx) error {
			return store.writeInstanceLease(t.Context(), tx, lease, now)
		}); err != nil {
			t.Fatalf("lease success=%v", err)
		}
		closeDB()
	}
}

func TestRuntimeReleaseClaimBranchStages(t *testing.T) {
	now := time.Now().UTC()
	claim := releaseClaim(now)
	baseQueries := []releaseSQLQueryStep{
		{columns: releaseColumns(3), rows: [][]driver.Value{{"", "", int64(-1)}}},
		{columns: releaseColumns(4)},
	}
	store, closeDB := scriptedReleaseStore(&releaseSQLState{
		execSteps:  []releaseSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}},
		querySteps: append([]releaseSQLQueryStep(nil), baseQueries...),
	})
	lease, err := store.claim(t.Context(), claim)
	if err != nil || lease.Generation != 1 {
		t.Fatalf("first claim lease=%#v err=%v", lease, err)
	}
	closeDB()

	for _, state := range []*releaseSQLState{
		{
			execSteps:  []releaseSQLExecStep{{rows: 1}, {err: errReleaseSQL}},
			querySteps: append([]releaseSQLQueryStep(nil), baseQueries...),
		},
		{
			execSteps:  []releaseSQLExecStep{{rows: 1}, {rows: 1}, {err: errReleaseSQL}},
			querySteps: append([]releaseSQLQueryStep(nil), baseQueries...),
		},
		{
			execSteps:  []releaseSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}},
			querySteps: append([]releaseSQLQueryStep(nil), baseQueries...),
			commitErr:  errReleaseSQL,
		},
	} {
		store, closeDB = scriptedReleaseStore(state)
		if _, err := store.claim(t.Context(), claim); err == nil {
			t.Fatal("claim write failure ignored")
		}
		closeDB()
	}

	for _, state := range []*releaseSQLState{
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{err: errReleaseSQL}}},
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{
			{columns: releaseColumns(3), rows: [][]driver.Value{{"hash", "{}", int64(1)}}},
			{err: errReleaseSQL},
		}},
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{
			{columns: releaseColumns(3), rows: [][]driver.Value{{"", "", int64(1)}}},
			{columns: releaseColumns(4), rows: [][]driver.Value{{"live", "hash", int64(1), now.Add(time.Minute).Format(time.RFC3339Nano)}}},
		}},
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{
			{columns: releaseColumns(3), rows: [][]driver.Value{{"hash", "{", int64(1)}}},
			{columns: releaseColumns(4), rows: [][]driver.Value{{"live", "hash", int64(1), now.Add(time.Minute).Format(time.RFC3339Nano)}}},
		}},
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{
			{columns: releaseColumns(3), rows: [][]driver.Value{{"hash", `{"contract_version":"v1","combination_sha256":"hash"}`, int64(1)}}},
			{columns: releaseColumns(4), rows: [][]driver.Value{{"live", "other", int64(2), now.Add(time.Minute).Format(time.RFC3339Nano)}}},
		}},
	} {
		store, closeDB = scriptedReleaseStore(state)
		if _, err := store.claim(t.Context(), claim); err == nil {
			t.Fatal("claim failure ignored")
		}
		closeDB()
	}
	identityJSON, _ := json.Marshal(claim.Identity)
	live := func(hash string, generation int64) releaseSQLQueryStep {
		return releaseSQLQueryStep{columns: releaseColumns(4), rows: [][]driver.Value{{"live", hash, generation, now.Add(time.Minute).Format(time.RFC3339Nano)}}}
	}
	for _, state := range []*releaseSQLState{
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3)}, live("hash", 1)}},
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3), rows: [][]driver.Value{{"", string(identityJSON), int64(1)}}}, live("", 1)}},
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3), rows: [][]driver.Value{{"hash", "", int64(1)}}}, live("hash", 1)}},
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3), rows: [][]driver.Value{{"hash", string(identityJSON), int64(1)}}}, live("other", 1)}},
		{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3), rows: [][]driver.Value{{"other", string(identityJSON), int64(1)}}}, live("other", 1)}},
	} {
		store, closeDB = scriptedReleaseStore(state)
		if _, err := store.claim(t.Context(), claim); err == nil {
			t.Fatal("short-circuit claim state was accepted")
		}
		closeDB()
	}
}

func TestRuntimeReleaseHeartbeatStages(t *testing.T) {
	now := time.Now().UTC()
	lease := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "instance", CombinationSHA256: "hash", Generation: 1}
	cohortRow := releaseSQLQueryStep{columns: releaseColumns(3), rows: [][]driver.Value{{"hash", "{}", int64(1)}}}
	futureRow := releaseSQLQueryStep{columns: []string{"expiry"}, rows: [][]driver.Value{{now.Add(time.Minute).Format(time.RFC3339Nano)}}}
	tests := []struct {
		name  string
		state *releaseSQLState
	}{
		{"begin", &releaseSQLState{beginErr: errReleaseSQL}},
		{"lock", &releaseSQLState{execSteps: []releaseSQLExecStep{{err: errReleaseSQL}}}},
		{"cohort", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3), rows: [][]driver.Value{{"other", "{}", int64(2)}}}}}},
		{"cohort missing", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3)}}}},
		{"cohort empty", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3), rows: [][]driver.Value{{"", "", int64(0)}}}}}},
		{"cohort hash", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{{columns: releaseColumns(3), rows: [][]driver.Value{{"other", "{}", int64(1)}}}}}},
		{"missing", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{cohortRow, {columns: []string{"expiry"}}}}},
		{"select", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{cohortRow, {err: errReleaseSQL}}}},
		{"malformed", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{cohortRow, {columns: []string{"expiry"}, rows: [][]driver.Value{{"bad"}}}}}},
		{"expired", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}}, querySteps: []releaseSQLQueryStep{cohortRow, {columns: []string{"expiry"}, rows: [][]driver.Value{{now.Format(time.RFC3339Nano)}}}}}},
		{"update", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}, {err: errReleaseSQL}}, querySteps: []releaseSQLQueryStep{cohortRow, futureRow}}},
		{"rows", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}, {rowsErr: errReleaseSQL}}, querySteps: []releaseSQLQueryStep{cohortRow, futureRow}}},
		{"lost", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}, {rows: 0}}, querySteps: []releaseSQLQueryStep{cohortRow, futureRow}}},
		{"commit", &releaseSQLState{execSteps: []releaseSQLExecStep{{rows: 1}, {rows: 1}}, querySteps: []releaseSQLQueryStep{cohortRow, futureRow}, commitErr: errReleaseSQL}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedReleaseStore(test.state)
			defer closeDB()
			if _, err := store.HeartbeatRuntimeRelease(t.Context(), lease, now, time.Minute); err == nil {
				t.Fatal("heartbeat failure ignored")
			}
		})
	}
	store, closeDB := scriptedReleaseStore(&releaseSQLState{
		execSteps: []releaseSQLExecStep{{rows: 1}, {rows: 1}}, querySteps: []releaseSQLQueryStep{cohortRow, futureRow},
	})
	next, err := store.HeartbeatRuntimeRelease(t.Context(), lease, now, time.Minute)
	if err != nil || !next.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("heartbeat=%#v err=%v", next, err)
	}
	closeDB()
}

func TestRuntimeReleaseReleaseAndSQLHelpers(t *testing.T) {
	store, closeDB := scriptedReleaseStore(&releaseSQLState{execSteps: []releaseSQLExecStep{{err: errReleaseSQL}}})
	if err := store.ReleaseRuntimeRelease(t.Context(), deploymentmodel.RuntimeReleaseCohortLease{}); err != errReleaseSQL {
		t.Fatalf("release error=%v", err)
	}
	closeDB()
	store, closeDB = scriptedReleaseStore(&releaseSQLState{})
	if err := store.ReleaseRuntimeRelease(t.Context(), deploymentmodel.RuntimeReleaseCohortLease{}); err != nil {
		t.Fatalf("release success=%v", err)
	}
	if store.quoted([]string{"a", "b"}) != `"a", "b"` || store.placeholders(2) != "?, ?" {
		t.Fatal("SQL helper output")
	}
	closeDB()
}
