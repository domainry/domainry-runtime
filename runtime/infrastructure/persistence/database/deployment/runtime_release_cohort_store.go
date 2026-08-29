package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
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

type runtimeReleaseRenderer struct{ store runtimeReleaseSQLStore }

func (r runtimeReleaseRenderer) Identifier(value string) string { return r.store.Identifier(value) }
func (r runtimeReleaseRenderer) Table(value string) string      { return r.store.TableIdentifier(value) }
func (r runtimeReleaseRenderer) Placeholder(position int) string {
	return r.store.Placeholder(position)
}

func (s RuntimeReleaseCohortStore) renderer() ormbuilder.Renderer {
	return runtimeReleaseRenderer{store: s.store}
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
	update, updateArgs, err := ormbuilder.NewUpdateBuilder(s.renderer(), "runtime_release_cohorts").SetExpression("revision", ormbuilder.Add(ormbuilder.Column("revision"), ormbuilder.Value(1))).Where(ormbuilder.Equal("cohort_key", activeRuntimeReleaseCohort)).Build()
	if err != nil {
		return runtimeReleaseCohortRow{}, false, err
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return runtimeReleaseCohortRow{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return runtimeReleaseCohortRow{}, false, err
	}
	if rows == 0 {
		query, args, buildErr := ormbuilder.NewInsertBuilder(s.renderer(), "runtime_release_cohorts").Columns("cohort_key", "combination_sha256", "identity_json", "generation", "revision", "updated_at").Values(activeRuntimeReleaseCohort, "", "", int64(0), int64(1), "").Build()
		if buildErr != nil {
			return runtimeReleaseCohortRow{}, false, buildErr
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return runtimeReleaseCohortRow{}, false, err
		}
	}
	query, args, err := ormbuilder.NewSelectBuilder(s.renderer(), "runtime_release_cohorts").Columns("combination_sha256", "identity_json", "generation").Where(ormbuilder.Equal("cohort_key", activeRuntimeReleaseCohort)).Build()
	if err != nil {
		return runtimeReleaseCohortRow{}, false, err
	}
	var row runtimeReleaseCohortRow
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&row.combinationSHA256, &row.identityJSON, &row.generation); err != nil {
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
	query, args, err := ormbuilder.NewSelectBuilder(s.renderer(), "runtime_release_instances").Columns("instance_id", "combination_sha256", "generation", "lease_expires_at").Build()
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, query, args...)
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
		statement, statementArgs, buildErr := ormbuilder.NewDeleteBuilder(s.renderer(), "runtime_release_instances").Where(ormbuilder.Equal("instance_id", instanceID)).Build()
		if buildErr != nil {
			return nil, buildErr
		}
		if _, err := tx.ExecContext(ctx, statement, statementArgs...); err != nil {
			return nil, err
		}
	}
	return live, nil
}

func (s RuntimeReleaseCohortStore) replaceCohort(ctx context.Context, tx *sql.Tx, generation int64, combination, identityJSON string, now time.Time) error {
	query, args, err := ormbuilder.NewUpdateBuilder(s.renderer(), "runtime_release_cohorts").Set("combination_sha256", combination).Set("identity_json", identityJSON).Set("generation", generation).Set("updated_at", now.UTC().Format(time.RFC3339Nano)).Where(ormbuilder.Equal("cohort_key", activeRuntimeReleaseCohort)).Build()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewUpdateBuilder(s.renderer(), "runtime_release_instances").Set("combination_sha256", lease.CombinationSHA256).Set("generation", lease.Generation).Set("lease_expires_at", lease.ExpiresAt.Format(time.RFC3339Nano)).Set("heartbeat_at", now.UTC().Format(time.RFC3339Nano)).Where(ormbuilder.Equal("instance_id", lease.InstanceID)).Build()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	insert, insertArgs, err := ormbuilder.NewInsertBuilder(s.renderer(), "runtime_release_instances").Columns("instance_id", "combination_sha256", "generation", "lease_expires_at", "joined_at", "heartbeat_at").Values(lease.InstanceID, lease.CombinationSHA256, lease.Generation, lease.ExpiresAt.Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano)).Build()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, insert, insertArgs...)
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
	leasePredicate := ormbuilder.And(ormbuilder.Equal("instance_id", lease.InstanceID), ormbuilder.Equal("combination_sha256", lease.CombinationSHA256), ormbuilder.Equal("generation", lease.Generation))
	selectQuery, selectArgs, buildErr := ormbuilder.NewSelectBuilder(s.renderer(), "runtime_release_instances").Columns("lease_expires_at").Where(leasePredicate).Build()
	if buildErr != nil {
		return lease, buildErr
	}
	var expiresAt string
	if err := tx.QueryRowContext(ctx, selectQuery, selectArgs...).Scan(&expiresAt); err != nil {
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
	query, args, buildErr := ormbuilder.NewUpdateBuilder(s.renderer(), "runtime_release_instances").Set("lease_expires_at", next.Format(time.RFC3339Nano)).Set("heartbeat_at", now.UTC().Format(time.RFC3339Nano)).Where(leasePredicate).Build()
	if buildErr != nil {
		return lease, buildErr
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewDeleteBuilder(s.renderer(), "runtime_release_instances").Where(ormbuilder.And(ormbuilder.Equal("instance_id", lease.InstanceID), ormbuilder.Equal("combination_sha256", lease.CombinationSHA256), ormbuilder.Equal("generation", lease.Generation))).Build()
	if err != nil {
		return err
	}
	_, err = s.store.DB().ExecContext(ctx, query, args...)
	return err
}

// quoted and placeholders remain as compatibility helpers for store-level
// diagnostics; production CRUD is rendered exclusively through ORM builders.
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
