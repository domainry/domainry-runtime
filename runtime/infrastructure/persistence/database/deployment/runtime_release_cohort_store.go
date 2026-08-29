package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
)

const activeRuntimeReleaseCohort = "active"

var _ deploymentrepository.RuntimeReleaseCohortRepository = RuntimeReleaseCohortStore{}

type RuntimeReleaseCohortStore struct {
	store runtimeReleaseSQLStore
}

type runtimeReleaseSQLStore interface {
	DB() *sql.DB
	TableIdentifier(string) string
	Identifier(string) string
	Placeholder(int) string
	IsCoordinationRetryableError(error) bool
}

func NewRuntimeReleaseCohortStore(store runtimeReleaseSQLStore) RuntimeReleaseCohortStore {
	return RuntimeReleaseCohortStore{store: store}
}

func (s RuntimeReleaseCohortStore) ClaimRuntimeRelease(ctx context.Context, claim deploymentmodel.RuntimeReleaseCohortClaim) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	if s.store == nil || s.store.DB() == nil {
		return deploymentmodel.RuntimeReleaseCohortLease{}, fmt.Errorf("%w: release cohort store is unavailable", deploymentmodel.ErrRuntimeReleaseAdmission)
	}
	for attempt := 0; attempt < 8; attempt++ {
		lease, err := s.claim(ctx, claim)
		if err == nil || errors.Is(err, deploymentmodel.ErrRuntimeReleaseConflict) || !s.store.IsCoordinationRetryableError(err) {
			return lease, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return deploymentmodel.RuntimeReleaseCohortLease{}, ctx.Err()
		case <-timer.C:
		}
	}
	return deploymentmodel.RuntimeReleaseCohortLease{}, fmt.Errorf("%w: release cohort serialization retry exhausted", deploymentmodel.ErrRuntimeReleaseAdmission)
}

func (s RuntimeReleaseCohortStore) claim(ctx context.Context, claim deploymentmodel.RuntimeReleaseCohortClaim) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return deploymentmodel.RuntimeReleaseCohortLease{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cohort, exists, err := s.lockCohort(ctx, tx)
	if err != nil {
		return deploymentmodel.RuntimeReleaseCohortLease{}, err
	}
	instances, err := s.liveInstances(ctx, tx, claim.Now)
	if err != nil {
		return deploymentmodel.RuntimeReleaseCohortLease{}, err
	}
	// RuntimeReleaseIdentity contains only JSON-safe scalar fields.
	identityJSON, _ := json.Marshal(claim.Identity)
	generation := cohort.generation
	if len(instances) == 0 {
		generation++
		if generation <= 0 {
			generation = 1
		}
		if err := s.replaceCohort(ctx, tx, generation, claim.Identity.CombinationSHA256, string(identityJSON), claim.Now); err != nil {
			return deploymentmodel.RuntimeReleaseCohortLease{}, err
		}
	} else {
		if !exists || cohort.identityJSON == "" {
			return deploymentmodel.RuntimeReleaseCohortLease{}, fmt.Errorf("%w: live instances have no active release cohort", deploymentmodel.ErrRuntimeReleaseAdmission)
		}
		var active deploymentmodel.RuntimeReleaseIdentity
		if err := json.Unmarshal([]byte(cohort.identityJSON), &active); err != nil {
			return deploymentmodel.RuntimeReleaseCohortLease{}, fmt.Errorf("%w: decode active release identity: %v", deploymentmodel.ErrRuntimeReleaseAdmission, err)
		}
		for _, instance := range instances {
			if instance.generation != cohort.generation || instance.combinationSHA256 != cohort.combinationSHA256 {
				return deploymentmodel.RuntimeReleaseCohortLease{}, fmt.Errorf("%w: active release lease disagrees with cohort", deploymentmodel.ErrRuntimeReleaseAdmission)
			}
		}
		if active != claim.Identity || cohort.combinationSHA256 != claim.Identity.CombinationSHA256 {
			return deploymentmodel.RuntimeReleaseCohortLease{}, deploymentmodel.RuntimeReleaseCohortConflict{Active: active, Joining: claim.Identity}
		}
	}
	lease := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: claim.InstanceID, CombinationSHA256: claim.Identity.CombinationSHA256, Generation: generation, ExpiresAt: claim.Now.Add(claim.LeaseDuration).UTC()}
	if err := s.writeInstanceLease(ctx, tx, lease, claim.Now); err != nil {
		return deploymentmodel.RuntimeReleaseCohortLease{}, err
	}
	if err := tx.Commit(); err != nil {
		return deploymentmodel.RuntimeReleaseCohortLease{}, err
	}
	return lease, nil
}

type runtimeReleaseCohortRow struct {
	combinationSHA256 string
	identityJSON      string
	generation        int64
}

func (s RuntimeReleaseCohortStore) lockCohort(ctx context.Context, tx *sql.Tx) (runtimeReleaseCohortRow, bool, error) {
	table := s.store.TableIdentifier("runtime_release_cohorts")
	update := "UPDATE " + table + " SET " + s.store.Identifier("revision") + " = " + s.store.Identifier("revision") + " + 1 WHERE " + s.store.Identifier("cohort_key") + " = " + s.store.Placeholder(1)
	result, err := tx.ExecContext(ctx, update, activeRuntimeReleaseCohort)
	if err != nil {
		return runtimeReleaseCohortRow{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return runtimeReleaseCohortRow{}, false, err
	}
	if rows == 0 {
		columns := []string{"cohort_key", "combination_sha256", "identity_json", "generation", "revision", "updated_at"}
		query := "INSERT INTO " + table + " (" + s.quoted(columns) + ") VALUES (" + s.placeholders(len(columns)) + ")"
		if _, err := tx.ExecContext(ctx, query, activeRuntimeReleaseCohort, "", "", int64(0), int64(1), ""); err != nil {
			return runtimeReleaseCohortRow{}, false, err
		}
	}
	query := "SELECT " + s.quoted([]string{"combination_sha256", "identity_json", "generation"}) + " FROM " + table + " WHERE " + s.store.Identifier("cohort_key") + " = " + s.store.Placeholder(1)
	var row runtimeReleaseCohortRow
	if err := tx.QueryRowContext(ctx, query, activeRuntimeReleaseCohort).Scan(&row.combinationSHA256, &row.identityJSON, &row.generation); err != nil {
		return runtimeReleaseCohortRow{}, false, err
	}
	return row, row.combinationSHA256 != "", nil
}

type runtimeReleaseInstanceRow struct {
	instanceID        string
	combinationSHA256 string
	generation        int64
	expiresAt         time.Time
}

func (s RuntimeReleaseCohortStore) liveInstances(ctx context.Context, tx *sql.Tx, now time.Time) ([]runtimeReleaseInstanceRow, error) {
	table := s.store.TableIdentifier("runtime_release_instances")
	query := "SELECT " + s.quoted([]string{"instance_id", "combination_sha256", "generation", "lease_expires_at"}) + " FROM " + table
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var live []runtimeReleaseInstanceRow
	var expired []string
	for rows.Next() {
		var row runtimeReleaseInstanceRow
		var expiresAt string
		if err := rows.Scan(&row.instanceID, &row.combinationSHA256, &row.generation, &expiresAt); err != nil {
			return nil, err
		}
		row.expiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
		if err != nil {
			return nil, fmt.Errorf("%w: malformed release lease expiry for %s", deploymentmodel.ErrRuntimeReleaseAdmission, row.instanceID)
		}
		if !row.expiresAt.After(now) {
			expired = append(expired, row.instanceID)
			continue
		}
		live = append(live, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, instanceID := range expired {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+s.store.Identifier("instance_id")+" = "+s.store.Placeholder(1), instanceID); err != nil {
			return nil, err
		}
	}
	return live, nil
}

func (s RuntimeReleaseCohortStore) replaceCohort(ctx context.Context, tx *sql.Tx, generation int64, combination, identityJSON string, now time.Time) error {
	query := "UPDATE " + s.store.TableIdentifier("runtime_release_cohorts") + " SET " + s.store.Identifier("combination_sha256") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("identity_json") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("generation") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(4) + " WHERE " + s.store.Identifier("cohort_key") + " = " + s.store.Placeholder(5)
	result, err := tx.ExecContext(ctx, query, combination, identityJSON, generation, now.UTC().Format(time.RFC3339Nano), activeRuntimeReleaseCohort)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("%w: replace release cohort affected %d rows: %v", deploymentmodel.ErrRuntimeReleaseAdmission, rows, err)
	}
	return nil
}

func (s RuntimeReleaseCohortStore) writeInstanceLease(ctx context.Context, tx *sql.Tx, lease deploymentmodel.RuntimeReleaseCohortLease, now time.Time) error {
	table := s.store.TableIdentifier("runtime_release_instances")
	query := "UPDATE " + table + " SET " + s.store.Identifier("combination_sha256") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("generation") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("lease_expires_at") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("heartbeat_at") + " = " + s.store.Placeholder(4) + " WHERE " + s.store.Identifier("instance_id") + " = " + s.store.Placeholder(5)
	result, err := tx.ExecContext(ctx, query, lease.CombinationSHA256, lease.Generation, lease.ExpiresAt.Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), lease.InstanceID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 1 {
		return nil
	}
	columns := []string{"instance_id", "combination_sha256", "generation", "lease_expires_at", "joined_at", "heartbeat_at"}
	insert := "INSERT INTO " + table + " (" + s.quoted(columns) + ") VALUES (" + s.placeholders(len(columns)) + ")"
	_, err = tx.ExecContext(ctx, insert, lease.InstanceID, lease.CombinationSHA256, lease.Generation, lease.ExpiresAt.Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano))
	return err
}

func (s RuntimeReleaseCohortStore) HeartbeatRuntimeRelease(ctx context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, now time.Time, duration time.Duration) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return lease, err
	}
	defer func() { _ = tx.Rollback() }()
	cohort, exists, err := s.lockCohort(ctx, tx)
	if err != nil {
		return lease, err
	}
	if !exists || cohort.generation != lease.Generation || cohort.combinationSHA256 != lease.CombinationSHA256 {
		return lease, fmt.Errorf("%w: active cohort changed", deploymentmodel.ErrRuntimeReleaseLeaseLost)
	}
	selectQuery := "SELECT " + s.store.Identifier("lease_expires_at") + " FROM " + s.store.TableIdentifier("runtime_release_instances") + " WHERE " + s.store.Identifier("instance_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("combination_sha256") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("generation") + " = " + s.store.Placeholder(3)
	var expiresAt string
	if err := tx.QueryRowContext(ctx, selectQuery, lease.InstanceID, lease.CombinationSHA256, lease.Generation).Scan(&expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return lease, fmt.Errorf("%w: instance=%s generation=%d", deploymentmodel.ErrRuntimeReleaseLeaseLost, lease.InstanceID, lease.Generation)
		}
		return lease, err
	}
	currentExpiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || !currentExpiry.After(now) {
		return lease, fmt.Errorf("%w: instance=%s lease expired", deploymentmodel.ErrRuntimeReleaseLeaseLost, lease.InstanceID)
	}
	next := now.Add(duration).UTC()
	query := "UPDATE " + s.store.TableIdentifier("runtime_release_instances") + " SET " + s.store.Identifier("lease_expires_at") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("heartbeat_at") + " = " + s.store.Placeholder(2) + " WHERE " + s.store.Identifier("instance_id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("combination_sha256") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("generation") + " = " + s.store.Placeholder(5)
	result, err := tx.ExecContext(ctx, query, next.Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), lease.InstanceID, lease.CombinationSHA256, lease.Generation)
	if err != nil {
		return lease, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return lease, err
	}
	if rows != 1 {
		return lease, fmt.Errorf("%w: instance=%s generation=%d", deploymentmodel.ErrRuntimeReleaseLeaseLost, lease.InstanceID, lease.Generation)
	}
	if err := tx.Commit(); err != nil {
		return lease, err
	}
	lease.ExpiresAt = next
	return lease, nil
}

func (s RuntimeReleaseCohortStore) ReleaseRuntimeRelease(ctx context.Context, lease deploymentmodel.RuntimeReleaseCohortLease) error {
	query := "DELETE FROM " + s.store.TableIdentifier("runtime_release_instances") + " WHERE " + s.store.Identifier("instance_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("combination_sha256") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("generation") + " = " + s.store.Placeholder(3)
	_, err := s.store.DB().ExecContext(ctx, query, lease.InstanceID, lease.CombinationSHA256, lease.Generation)
	return err
}

func (s RuntimeReleaseCohortStore) quoted(columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = s.store.Identifier(column)
	}
	return strings.Join(quoted, ", ")
}

func (s RuntimeReleaseCohortStore) placeholders(count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = s.store.Placeholder(index + 1)
	}
	return strings.Join(values, ", ")
}
