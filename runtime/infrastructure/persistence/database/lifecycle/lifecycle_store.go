package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type LifecycleStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewLifecycleStore(store *database.RuntimeStore) LifecycleStore {
	return LifecycleStore{store: store, db: store.DB()}
}

func (s LifecycleStore) SavePolicy(ctx context.Context, version lifecyclemodel.PolicyVersion) error {
	payload, _ := json.Marshal(version)
	query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "lifecycle_policy_versions", version.WorkspaceID).
		Columns("policy_key", "version", "revision", "status", "payload_json", "published_at").
		Values(version.Policy.Key, version.Policy.Version, version.Revision, version.Status, string(payload), lifecycleTime(version.PublishedAt)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle policy insert: %w", buildErr)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("save lifecycle policy: %w", err)
	}
	return nil
}

func (s LifecycleStore) LatestPolicy(ctx context.Context, workspaceID, policyKey string) (lifecyclemodel.PolicyVersion, bool, error) {
	version, found, err := s.latestPolicyForWorkspace(ctx, workspaceID, policyKey)
	if err != nil || found || workspaceID == principalmodel.InstallationWorkspaceID {
		return version, found, err
	}
	return s.latestPolicyForWorkspace(ctx, principalmodel.InstallationWorkspaceID, policyKey)
}

func (s LifecycleStore) latestPolicyForWorkspace(ctx context.Context, workspaceID, policyKey string) (lifecyclemodel.PolicyVersion, bool, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_policy_versions", workspaceID).Columns("payload_json").
		Where(ormbuilder.And(ormbuilder.Equal("policy_key", policyKey), ormbuilder.Equal("status", lifecyclemodel.PolicyStatusPublished))).
		OrderBy(ormbuilder.Descending("revision")).Limit(1).Build()
	if buildErr != nil {
		return lifecyclemodel.PolicyVersion{}, false, fmt.Errorf("build lifecycle policy query: %w", buildErr)
	}
	var payload string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return lifecyclemodel.PolicyVersion{}, false, nil
	}
	if err != nil {
		return lifecyclemodel.PolicyVersion{}, false, fmt.Errorf("load lifecycle policy: %w", err)
	}
	var version lifecyclemodel.PolicyVersion
	if err := json.Unmarshal([]byte(payload), &version); err != nil {
		return lifecyclemodel.PolicyVersion{}, false, err
	}
	return version, true, nil
}

func (s LifecycleStore) ListPolicies(ctx context.Context, workspaceID string) ([]lifecyclemodel.PolicyVersion, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_policy_versions", workspaceID).Columns("payload_json").
		OrderBy(ormbuilder.Ascending("policy_key"), ormbuilder.Descending("revision")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build lifecycle policy list: %w", buildErr)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []lifecyclemodel.PolicyVersion{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var version lifecyclemodel.PolicyVersion
		if err := json.Unmarshal([]byte(payload), &version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s LifecycleStore) SaveLegalHold(ctx context.Context, hold lifecyclemodel.LegalHold) error {
	payload, _ := json.Marshal(hold)
	ends := ""
	if hold.EndsAt != nil {
		ends = lifecycleTime(*hold.EndsAt)
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "lifecycle_legal_holds", hold.WorkspaceID).
		Set("owner", hold.Owner).Set("resource_type", hold.ResourceType).Set("resource_id", hold.ResourceID).
		Set("starts_at", lifecycleTime(hold.StartsAt)).Set("ends_at", ends).Set("review_at", lifecycleTime(hold.ReviewAt)).Set("payload_json", string(payload)).
		Where(ormbuilder.Equal("id", hold.ID)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle legal hold update: %w", buildErr)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated lifecycle legal hold count: %w", err)
	}
	if changed > 0 {
		return nil
	}
	query, args, buildErr = ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "lifecycle_legal_holds", hold.WorkspaceID).
		Columns("id", "owner", "resource_type", "resource_id", "starts_at", "ends_at", "review_at", "payload_json").
		Values(hold.ID, hold.Owner, hold.ResourceType, hold.ResourceID, lifecycleTime(hold.StartsAt), ends, lifecycleTime(hold.ReviewAt), string(payload)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle legal hold insert: %w", buildErr)
	}
	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("save lifecycle legal hold: %w", err)
	}
	return nil
}

func (s LifecycleStore) GetLegalHold(ctx context.Context, workspaceID, holdID string) (lifecyclemodel.LegalHold, bool, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_legal_holds", workspaceID).Columns("payload_json").Where(ormbuilder.Equal("id", holdID)).Build()
	if buildErr != nil {
		return lifecyclemodel.LegalHold{}, false, fmt.Errorf("build lifecycle legal hold query: %w", buildErr)
	}
	var payload string
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&payload); errors.Is(err, sql.ErrNoRows) {
		return lifecyclemodel.LegalHold{}, false, nil
	} else if err != nil {
		return lifecyclemodel.LegalHold{}, false, err
	}
	var hold lifecyclemodel.LegalHold
	if err := json.Unmarshal([]byte(payload), &hold); err != nil {
		return lifecyclemodel.LegalHold{}, false, err
	}
	return hold, true, nil
}

func (s LifecycleStore) ActiveLegalHolds(ctx context.Context, target lifecyclemodel.ResourceTarget, now time.Time) ([]lifecyclemodel.LegalHold, error) {
	nowText := lifecycleTime(now)
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_legal_holds", target.WorkspaceID).Columns("payload_json").Where(ormbuilder.And(
		ormbuilder.LessThanOrEqual("starts_at", nowText), ormbuilder.Or(ormbuilder.Equal("ends_at", ""), ormbuilder.GreaterThan("ends_at", nowText)),
	)).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build active lifecycle legal holds query: %w", buildErr)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	holds := []lifecyclemodel.LegalHold{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var hold lifecyclemodel.LegalHold
		if err := json.Unmarshal([]byte(payload), &hold); err != nil {
			return nil, err
		}
		if (target.Owner == "" || hold.Owner == "" || hold.Owner == target.Owner) && (target.ResourceType == "" || hold.ResourceType == "" || hold.ResourceType == target.ResourceType) && (target.ResourceID == "*" || target.ResourceID == "" || hold.ResourceID == "" || hold.ResourceID == target.ResourceID) {
			holds = append(holds, hold)
		}
	}
	return holds, rows.Err()
}

func (s LifecycleStore) SaveCleanupJob(ctx context.Context, job lifecyclemodel.CleanupJob) error {
	payload, _ := json.Marshal(job)
	query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "lifecycle_cleanup_jobs", job.WorkspaceID).
		Columns("id", "policy_key", "policy_version", "status", "checkpoint_value", "lease_owner", "lease_expires_at", "fencing_token", "updated_at", "payload_json").
		Values(job.ID, job.PolicyKey, job.PolicyVersion, job.Status, job.Checkpoint, job.LeaseOwner, lifecycleTime(job.LeaseExpiresAt), job.FencingToken, lifecycleTime(job.UpdatedAt), string(payload)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle cleanup job insert: %w", buildErr)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("save lifecycle cleanup job: %w", err)
	}
	return nil
}

func (s LifecycleStore) GetCleanupJob(ctx context.Context, workspaceID, id string) (lifecyclemodel.CleanupJob, bool, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_cleanup_jobs", workspaceID).
		Columns("status", "checkpoint_value", "lease_owner", "lease_expires_at", "fencing_token", "updated_at", "payload_json").Where(ormbuilder.Equal("id", id)).Build()
	if buildErr != nil {
		return lifecyclemodel.CleanupJob{}, false, fmt.Errorf("build lifecycle cleanup job query: %w", buildErr)
	}
	var status, checkpoint, leaseOwner, leaseExpiresAt, updatedAt, payload string
	var fencingToken int64
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&status, &checkpoint, &leaseOwner, &leaseExpiresAt, &fencingToken, &updatedAt, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return lifecyclemodel.CleanupJob{}, false, nil
	}
	if err != nil {
		return lifecyclemodel.CleanupJob{}, false, err
	}
	var job lifecyclemodel.CleanupJob
	if err := json.Unmarshal([]byte(payload), &job); err != nil {
		return lifecyclemodel.CleanupJob{}, false, err
	}
	job.Status = lifecyclemodel.CleanupStatus(status)
	job.Checkpoint, job.LeaseOwner, job.LeaseExpiresAt = checkpoint, leaseOwner, parseLifecycleTime(leaseExpiresAt)
	job.FencingToken, job.UpdatedAt = fencingToken, parseLifecycleTime(updatedAt)
	return job, true, nil
}

func (s LifecycleStore) ListRunnableCleanupJobs(ctx context.Context, scope principalmodel.SystemScope, limit int, now time.Time) ([]lifecyclemodel.CleanupJob, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "lifecycle_cleanup_jobs").Columns("payload_json").Where(ormbuilder.And(
		ormbuilder.In("status", lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed, lifecyclemodel.CleanupStatusRunning),
		ormbuilder.Or(ormbuilder.Equal("lease_owner", ""), ormbuilder.LessThanOrEqual("lease_expires_at", lifecycleTime(now))),
	)).OrderBy(ormbuilder.Ascending("updated_at")).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build runnable lifecycle cleanup query: %w", buildErr)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []lifecyclemodel.CleanupJob{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var job lifecyclemodel.CleanupJob
		if err := json.Unmarshal([]byte(payload), &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s LifecycleStore) ClaimCleanupJob(ctx context.Context, workspaceID, id, owner string, ttl time.Duration, now time.Time) (lifecyclemodel.CleanupJob, bool, error) {
	if strings.TrimSpace(owner) == "" {
		return lifecyclemodel.CleanupJob{}, false, fmt.Errorf("cleanup lease owner required")
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "lifecycle_cleanup_jobs", workspaceID).
		Set("status", lifecyclemodel.CleanupStatusRunning).Set("lease_owner", owner).Set("lease_expires_at", lifecycleTime(now.Add(ttl))).
		SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", lifecycleTime(now)).
		Where(ormbuilder.And(ormbuilder.Equal("id", id), ormbuilder.In("status", lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed, lifecyclemodel.CleanupStatusRunning),
			ormbuilder.Or(ormbuilder.Equal("lease_owner", ""), ormbuilder.LessThanOrEqual("lease_expires_at", lifecycleTime(now))))).Build()
	if buildErr != nil {
		return lifecyclemodel.CleanupJob{}, false, fmt.Errorf("build lifecycle cleanup claim: %w", buildErr)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return lifecyclemodel.CleanupJob{}, false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return lifecyclemodel.CleanupJob{}, false, err
	}
	job, found, err := s.GetCleanupJob(ctx, workspaceID, id)
	if err != nil || !found {
		return job, false, err
	}
	return job, true, nil
}

func (s LifecycleStore) UpdateCleanupJob(ctx context.Context, job lifecyclemodel.CleanupJob) error {
	payload, _ := json.Marshal(job)
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "lifecycle_cleanup_jobs", job.WorkspaceID).
		Set("status", job.Status).Set("checkpoint_value", job.Checkpoint).Set("lease_owner", job.LeaseOwner).
		Set("lease_expires_at", lifecycleTime(job.LeaseExpiresAt)).Set("updated_at", lifecycleTime(job.UpdatedAt)).Set("payload_json", string(payload)).
		Where(ormbuilder.And(ormbuilder.Equal("id", job.ID), ormbuilder.Equal("fencing_token", job.FencingToken))).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle cleanup update: %w", buildErr)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated lifecycle cleanup job count: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("lifecycle cleanup lease lost")
	}
	return nil
}

func (s LifecycleStore) SaveSubjectRequest(ctx context.Context, request lifecyclemodel.SubjectRequest) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "lifecycle_subject_requests", request.WorkspaceID).
		Set("kind", request.Kind).Set("status", request.Status).Set("subject_id", request.SubjectID).Set("resolved_identity", request.ResolvedIdentity).
		Set("download_expires_at", lifecycleTime(request.DownloadExpiresAt)).Set("updated_at", lifecycleTime(request.UpdatedAt)).Set("payload_json", string(payload)).
		Where(ormbuilder.Equal("id", request.ID)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle subject request update: %w", buildErr)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated lifecycle subject request count: %w", err)
	}
	if count == 1 {
		return nil
	}
	query, args, buildErr = ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "lifecycle_subject_requests", request.WorkspaceID).
		Columns("id", "kind", "status", "subject_id", "resolved_identity", "download_expires_at", "updated_at", "payload_json").
		Values(request.ID, request.Kind, request.Status, request.SubjectID, request.ResolvedIdentity, lifecycleTime(request.DownloadExpiresAt), lifecycleTime(request.UpdatedAt), string(payload)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle subject request insert: %w", buildErr)
	}
	_, err = s.db.ExecContext(ctx, query, args...)
	return err
}

func (s LifecycleStore) TransitionSubjectRequest(ctx context.Context, current, next lifecyclemodel.SubjectRequest) error {
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "lifecycle_subject_requests", next.WorkspaceID).
		Set("kind", next.Kind).Set("status", next.Status).Set("subject_id", next.SubjectID).Set("resolved_identity", next.ResolvedIdentity).
		Set("download_expires_at", lifecycleTime(next.DownloadExpiresAt)).Set("updated_at", lifecycleTime(next.UpdatedAt)).Set("payload_json", string(payload)).
		Where(ormbuilder.And(ormbuilder.Equal("id", next.ID), ormbuilder.Equal("status", current.Status), ormbuilder.Equal("updated_at", lifecycleTime(current.UpdatedAt)))).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle subject transition: %w", buildErr)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read transitioned lifecycle subject request count: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("subject request transition lost")
	}
	return nil
}

func (s LifecycleStore) GetSubjectRequest(ctx context.Context, workspaceID, id string) (lifecyclemodel.SubjectRequest, bool, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_subject_requests", workspaceID).Columns("payload_json").Where(ormbuilder.Equal("id", id)).Build()
	if buildErr != nil {
		return lifecyclemodel.SubjectRequest{}, false, fmt.Errorf("build lifecycle subject request query: %w", buildErr)
	}
	var payload string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return lifecyclemodel.SubjectRequest{}, false, nil
	}
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, false, err
	}
	var request lifecyclemodel.SubjectRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return lifecyclemodel.SubjectRequest{}, false, err
	}
	return request, true, nil
}

func (s LifecycleStore) ExpireSubjectExportReferences(ctx context.Context, scope principalmodel.SystemScope, now time.Time) ([]lifecyclemodel.SubjectRequest, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return nil, err
	}
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "lifecycle_subject_requests").Columns("payload_json").Where(ormbuilder.And(
		ormbuilder.Equal("kind", lifecyclemodel.SubjectRequestExport), ormbuilder.Equal("status", lifecyclemodel.SubjectRequestSucceeded),
		ormbuilder.NotEqual("download_expires_at", ""), ormbuilder.LessThanOrEqual("download_expires_at", lifecycleTime(now)),
	)).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build expired lifecycle subject export query: %w", buildErr)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	requests := []lifecyclemodel.SubjectRequest{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var request lifecyclemodel.SubjectRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if request.ResultReference != "" {
			request.ResultReference, request.UpdatedAt = "", now
			requests = append(requests, request)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	for _, request := range requests {
		if err := s.SaveSubjectRequest(ctx, request); err != nil {
			return nil, err
		}
	}
	return requests, nil
}

func (s LifecycleStore) SaveExternalErasures(ctx context.Context, erasures []lifecyclemodel.ExternalErasure) error {
	for _, erasure := range erasures {
		var existing int
		query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_external_erasures", erasure.WorkspaceID).
			Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.Equal("id", erasure.ID)).Build()
		if buildErr != nil {
			return fmt.Errorf("build external erasure lookup: %w", buildErr)
		}
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			continue
		}
		payload, _ := json.Marshal(erasure)
		query, args, buildErr = ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "lifecycle_external_erasures", erasure.WorkspaceID).
			Columns("id", "request_id", "status", "payload_json").Values(erasure.ID, erasure.RequestID, erasure.Status, string(payload)).Build()
		if buildErr != nil {
			return fmt.Errorf("build external erasure insert: %w", buildErr)
		}
		if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

func (s LifecycleStore) ListExternalErasures(ctx context.Context, workspaceID, requestID string) ([]lifecyclemodel.ExternalErasure, error) {
	selectBuilder := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_external_erasures", workspaceID).Columns("payload_json")
	if strings.TrimSpace(requestID) != "" {
		selectBuilder.Where(ormbuilder.Equal("request_id", strings.TrimSpace(requestID)))
	}
	query, args, buildErr := selectBuilder.Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build external erasure list: %w", buildErr)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []lifecyclemodel.ExternalErasure{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var item lifecyclemodel.ExternalErasure
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s LifecycleStore) ReconcileExternalErasure(ctx context.Context, workspaceID, id, evidence string, at time.Time) (lifecyclemodel.ExternalErasure, bool, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_external_erasures", workspaceID).Columns("payload_json").Where(ormbuilder.Equal("id", id)).Build()
	if buildErr != nil {
		return lifecyclemodel.ExternalErasure{}, false, fmt.Errorf("build external erasure query: %w", buildErr)
	}
	var payload string
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&payload); errors.Is(err, sql.ErrNoRows) {
		return lifecyclemodel.ExternalErasure{}, false, nil
	} else if err != nil {
		return lifecyclemodel.ExternalErasure{}, false, err
	}
	var item lifecyclemodel.ExternalErasure
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		return lifecyclemodel.ExternalErasure{}, false, err
	}
	item.Status, item.Evidence, item.ReconciledAt = "reconciled", strings.TrimSpace(evidence), at
	updated, _ := json.Marshal(item)
	query, args, buildErr = ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "lifecycle_external_erasures", workspaceID).
		Set("status", item.Status).Set("payload_json", string(updated)).Where(ormbuilder.Equal("id", id)).Build()
	if buildErr != nil {
		return lifecyclemodel.ExternalErasure{}, false, fmt.Errorf("build external erasure reconciliation: %w", buildErr)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return lifecyclemodel.ExternalErasure{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return lifecyclemodel.ExternalErasure{}, false, fmt.Errorf("read reconciled external erasure count: %w", err)
	}
	return item, changed == 1, nil
}

func (s LifecycleStore) SaveDeletionRegistration(ctx context.Context, registration lifecyclemodel.DeletionRegistration) error {
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "lifecycle_deletion_registry", registration.WorkspaceID).
		Set("resolved_identity", registration.ResolvedIdentity).Set("backup_pending", registration.BackupPending).Set("evidence", registration.Evidence).
		Set("updated_at", lifecycleTime(registration.UpdatedAt)).Where(ormbuilder.Equal("request_id", registration.RequestID)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle deletion registration update: %w", buildErr)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated lifecycle deletion registration count: %w", err)
	}
	if changed > 0 {
		return nil
	}
	query, args, buildErr = ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "lifecycle_deletion_registry", registration.WorkspaceID).
		Columns("request_id", "resolved_identity", "backup_pending", "evidence", "updated_at").
		Values(registration.RequestID, registration.ResolvedIdentity, registration.BackupPending, registration.Evidence, lifecycleTime(registration.UpdatedAt)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle deletion registration insert: %w", buildErr)
	}
	_, err = s.db.ExecContext(ctx, query, args...)
	return err
}

func (s LifecycleStore) ListPendingDeletionRegistrations(ctx context.Context, workspaceID string, limit int) ([]lifecyclemodel.DeletionRegistration, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_deletion_registry", workspaceID).
		Columns("request_id", "workspace_id", "resolved_identity", "backup_pending", "evidence", "updated_at").Where(ormbuilder.Equal("backup_pending", true)).
		OrderBy(ormbuilder.Ascending("updated_at")).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build pending lifecycle deletion registrations: %w", buildErr)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	registrations := []lifecyclemodel.DeletionRegistration{}
	for rows.Next() {
		var item lifecyclemodel.DeletionRegistration
		var updatedAt string
		if err := rows.Scan(&item.RequestID, &item.WorkspaceID, &item.ResolvedIdentity, &item.BackupPending, &item.Evidence, &updatedAt); err != nil {
			return nil, err
		}
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		registrations = append(registrations, item)
	}
	return registrations, rows.Err()
}

func (s LifecycleStore) ListArchiveEntries(ctx context.Context, workspaceID, sourceTable string, limit int) ([]lifecyclemodel.ArchiveEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	selectBuilder := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_archive_entries", workspaceID).
		Columns("id", "workspace_id", "owner", "source_table", "resource_id", "policy_key", "policy_version", "job_id", "payload_hash", "payload_json", "archived_at")
	if strings.TrimSpace(sourceTable) != "" {
		selectBuilder.Where(ormbuilder.Equal("source_table", strings.TrimSpace(sourceTable)))
	}
	query, args, buildErr := selectBuilder.OrderBy(ormbuilder.Descending("archived_at")).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build lifecycle archive entries query: %w", buildErr)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []lifecyclemodel.ArchiveEntry{}
	for rows.Next() {
		var entry lifecyclemodel.ArchiveEntry
		var payload, archivedAt string
		if err := rows.Scan(&entry.ID, &entry.WorkspaceID, &entry.Owner, &entry.SourceTable, &entry.ResourceID, &entry.PolicyKey, &entry.PolicyVersion, &entry.JobID, &entry.PayloadHash, &payload, &archivedAt); err != nil {
			return nil, err
		}
		entry.Payload, entry.ArchivedAt = json.RawMessage(payload), parseLifecycleTime(archivedAt)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s LifecycleStore) AppendAuditEvidence(ctx context.Context, evidence lifecyclemodel.AuditEvidence) error {
	payload, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "lifecycle_audit_evidence", evidence.WorkspaceID).
		Columns("id", "event", "resource_id", "policy_key", "created_at", "payload_json").
		Values(evidence.ID, evidence.Event, evidence.ResourceID, evidence.PolicyKey, lifecycleTime(evidence.CreatedAt), string(payload)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle audit evidence insert: %w", buildErr)
	}
	_, err = s.db.ExecContext(ctx, query, args...)
	return err
}
