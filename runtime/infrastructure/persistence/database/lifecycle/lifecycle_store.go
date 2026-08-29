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
	columns := []string{"workspace_id", "policy_key", "version", "revision", "status", "payload_json", "published_at"}
	_, err := s.db.ExecContext(ctx, s.store.InsertStatement("lifecycle_policy_versions", columns), version.WorkspaceID, version.Policy.Key, version.Policy.Version, version.Revision, version.Status, string(payload), lifecycleTime(version.PublishedAt))
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
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("lifecycle_policy_versions") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("policy_key") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(3) + " ORDER BY " + s.store.Identifier("revision") + " DESC LIMIT 1"
	var payload string
	err := s.db.QueryRowContext(ctx, query, workspaceID, policyKey, lifecyclemodel.PolicyStatusPublished).Scan(&payload)
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
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("lifecycle_policy_versions") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " ORDER BY " + s.store.Identifier("policy_key") + ", " + s.store.Identifier("revision") + " DESC"
	rows, err := s.db.QueryContext(ctx, query, workspaceID)
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
	query := "UPDATE " + s.store.TableIdentifier("lifecycle_legal_holds") + " SET " + s.store.Identifier("owner") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("resource_type") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("resource_id") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("starts_at") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("ends_at") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("review_at") + " = " + s.store.Placeholder(6) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(7) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(9)
	result, err := s.db.ExecContext(ctx, query, hold.Owner, hold.ResourceType, hold.ResourceID, lifecycleTime(hold.StartsAt), ends, lifecycleTime(hold.ReviewAt), string(payload), hold.WorkspaceID, hold.ID)
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
	columns := []string{"id", "workspace_id", "owner", "resource_type", "resource_id", "starts_at", "ends_at", "review_at", "payload_json"}
	_, err = s.db.ExecContext(ctx, s.store.InsertStatement("lifecycle_legal_holds", columns), hold.ID, hold.WorkspaceID, hold.Owner, hold.ResourceType, hold.ResourceID, lifecycleTime(hold.StartsAt), ends, lifecycleTime(hold.ReviewAt), string(payload))
	if err != nil {
		return fmt.Errorf("save lifecycle legal hold: %w", err)
	}
	return nil
}

func (s LifecycleStore) GetLegalHold(ctx context.Context, workspaceID, holdID string) (lifecyclemodel.LegalHold, bool, error) {
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("lifecycle_legal_holds") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(2)
	var payload string
	if err := s.db.QueryRowContext(ctx, query, workspaceID, holdID).Scan(&payload); errors.Is(err, sql.ErrNoRows) {
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
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("lifecycle_legal_holds") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("starts_at") + " <= " + s.store.Placeholder(2) + " AND (" + s.store.Identifier("ends_at") + " = '' OR " + s.store.Identifier("ends_at") + " > " + s.store.Placeholder(3) + ")"
	rows, err := s.db.QueryContext(ctx, query, target.WorkspaceID, nowText, nowText)
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
	columns := []string{"id", "workspace_id", "policy_key", "policy_version", "status", "checkpoint_value", "lease_owner", "lease_expires_at", "fencing_token", "updated_at", "payload_json"}
	_, err := s.db.ExecContext(ctx, s.store.InsertStatement("lifecycle_cleanup_jobs", columns), job.ID, job.WorkspaceID, job.PolicyKey, job.PolicyVersion, job.Status, job.Checkpoint, job.LeaseOwner, lifecycleTime(job.LeaseExpiresAt), job.FencingToken, lifecycleTime(job.UpdatedAt), string(payload))
	if err != nil {
		return fmt.Errorf("save lifecycle cleanup job: %w", err)
	}
	return nil
}

func (s LifecycleStore) GetCleanupJob(ctx context.Context, workspaceID, id string) (lifecyclemodel.CleanupJob, bool, error) {
	query := "SELECT " + lifecycleColumns(s.store, "status", "checkpoint_value", "lease_owner", "lease_expires_at", "fencing_token", "updated_at", "payload_json") + " FROM " + s.store.TableIdentifier("lifecycle_cleanup_jobs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(2)
	var status, checkpoint, leaseOwner, leaseExpiresAt, updatedAt, payload string
	var fencingToken int64
	err := s.db.QueryRowContext(ctx, query, workspaceID, id).Scan(&status, &checkpoint, &leaseOwner, &leaseExpiresAt, &fencingToken, &updatedAt, &payload)
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
	query := "UPDATE " + s.store.TableIdentifier("lifecycle_cleanup_jobs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("lease_expires_at") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("fencing_token") + " = " + s.store.Identifier("fencing_token") + " + 1, " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(4) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("status") + " IN (" + s.store.Placeholder(7) + ", " + s.store.Placeholder(8) + ", " + s.store.Placeholder(9) + ", " + s.store.Placeholder(10) + ") AND (" + s.store.Identifier("lease_owner") + " = '' OR " + s.store.Identifier("lease_expires_at") + " <= " + s.store.Placeholder(11) + ")"
	result, err := s.db.ExecContext(ctx, query, lifecyclemodel.CleanupStatusRunning, owner, lifecycleTime(now.Add(ttl)), lifecycleTime(now), workspaceID, id, lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed, lifecyclemodel.CleanupStatusRunning, lifecycleTime(now))
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
	query := "UPDATE " + s.store.TableIdentifier("lifecycle_cleanup_jobs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("checkpoint_value") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("lease_expires_at") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(6) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(9)
	result, err := s.db.ExecContext(ctx, query, job.Status, job.Checkpoint, job.LeaseOwner, lifecycleTime(job.LeaseExpiresAt), lifecycleTime(job.UpdatedAt), string(payload), job.WorkspaceID, job.ID, job.FencingToken)
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
	query := "UPDATE " + s.store.TableIdentifier("lifecycle_subject_requests") + " SET " + s.store.Identifier("kind") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("status") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("subject_id") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("resolved_identity") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("download_expires_at") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(6) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(7) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(9)
	result, err := s.db.ExecContext(ctx, query, request.Kind, request.Status, request.SubjectID, request.ResolvedIdentity, lifecycleTime(request.DownloadExpiresAt), lifecycleTime(request.UpdatedAt), string(payload), request.WorkspaceID, request.ID)
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
	columns := []string{"id", "workspace_id", "kind", "status", "subject_id", "resolved_identity", "download_expires_at", "updated_at", "payload_json"}
	_, err = s.db.ExecContext(ctx, s.store.InsertStatement("lifecycle_subject_requests", columns), request.ID, request.WorkspaceID, request.Kind, request.Status, request.SubjectID, request.ResolvedIdentity, lifecycleTime(request.DownloadExpiresAt), lifecycleTime(request.UpdatedAt), string(payload))
	return err
}

func (s LifecycleStore) TransitionSubjectRequest(ctx context.Context, current, next lifecyclemodel.SubjectRequest) error {
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	query := "UPDATE " + s.store.TableIdentifier("lifecycle_subject_requests") + " SET " + s.store.Identifier("kind") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("status") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("subject_id") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("resolved_identity") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("download_expires_at") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(6) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(7) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(9) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(10) + " AND " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(11)
	result, err := s.db.ExecContext(ctx, query, next.Kind, next.Status, next.SubjectID, next.ResolvedIdentity, lifecycleTime(next.DownloadExpiresAt), lifecycleTime(next.UpdatedAt), string(payload), next.WorkspaceID, next.ID, current.Status, lifecycleTime(current.UpdatedAt))
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
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("lifecycle_subject_requests") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(2)
	var payload string
	err := s.db.QueryRowContext(ctx, query, workspaceID, id).Scan(&payload)
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
	columns := []string{"id", "request_id", "workspace_id", "status", "payload_json"}
	for _, erasure := range erasures {
		var existing int
		query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("lifecycle_external_erasures") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(2)
		if err := s.db.QueryRowContext(ctx, query, erasure.WorkspaceID, erasure.ID).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			continue
		}
		payload, _ := json.Marshal(erasure)
		if _, err := s.db.ExecContext(ctx, s.store.InsertStatement("lifecycle_external_erasures", columns), erasure.ID, erasure.RequestID, erasure.WorkspaceID, erasure.Status, string(payload)); err != nil {
			return err
		}
	}
	return nil
}

func (s LifecycleStore) ListExternalErasures(ctx context.Context, workspaceID, requestID string) ([]lifecyclemodel.ExternalErasure, error) {
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("lifecycle_external_erasures") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1)
	args := []any{workspaceID}
	if strings.TrimSpace(requestID) != "" {
		query += " AND " + s.store.Identifier("request_id") + " = " + s.store.Placeholder(2)
		args = append(args, strings.TrimSpace(requestID))
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
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("lifecycle_external_erasures") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(2)
	var payload string
	if err := s.db.QueryRowContext(ctx, query, workspaceID, id).Scan(&payload); errors.Is(err, sql.ErrNoRows) {
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
	result, err := s.db.ExecContext(ctx, "UPDATE "+s.store.TableIdentifier("lifecycle_external_erasures")+" SET "+s.store.Identifier("status")+" = "+s.store.Placeholder(1)+", "+s.store.Identifier("payload_json")+" = "+s.store.Placeholder(2)+" WHERE "+s.store.Identifier("workspace_id")+" = "+s.store.Placeholder(3)+" AND "+s.store.Identifier("id")+" = "+s.store.Placeholder(4), item.Status, string(updated), workspaceID, id)
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
	query := "UPDATE " + s.store.TableIdentifier("lifecycle_deletion_registry") + " SET " + s.store.Identifier("resolved_identity") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("backup_pending") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("evidence") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(4) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("request_id") + " = " + s.store.Placeholder(6)
	result, err := s.db.ExecContext(ctx, query, registration.ResolvedIdentity, registration.BackupPending, registration.Evidence, lifecycleTime(registration.UpdatedAt), registration.WorkspaceID, registration.RequestID)
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
	columns := []string{"request_id", "workspace_id", "resolved_identity", "backup_pending", "evidence", "updated_at"}
	_, err = s.db.ExecContext(ctx, s.store.InsertStatement("lifecycle_deletion_registry", columns), registration.RequestID, registration.WorkspaceID, registration.ResolvedIdentity, registration.BackupPending, registration.Evidence, lifecycleTime(registration.UpdatedAt))
	return err
}

func (s LifecycleStore) ListPendingDeletionRegistrations(ctx context.Context, workspaceID string, limit int) ([]lifecyclemodel.DeletionRegistration, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query := "SELECT " + s.store.Identifier("request_id") + ", " + s.store.Identifier("workspace_id") + ", " + s.store.Identifier("resolved_identity") + ", " + s.store.Identifier("backup_pending") + ", " + s.store.Identifier("evidence") + ", " + s.store.Identifier("updated_at") + " FROM " + s.store.TableIdentifier("lifecycle_deletion_registry") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("backup_pending") + " = " + s.store.Placeholder(2) + " ORDER BY " + s.store.Identifier("updated_at") + " LIMIT " + fmt.Sprintf("%d", limit)
	rows, err := s.db.QueryContext(ctx, query, workspaceID, true)
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
	query := "SELECT " + lifecycleColumns(s.store, "id", "workspace_id", "owner", "source_table", "resource_id", "policy_key", "policy_version", "job_id", "payload_hash", "payload_json", "archived_at") + " FROM " + s.store.TableIdentifier("lifecycle_archive_entries") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1)
	args := []any{workspaceID}
	if strings.TrimSpace(sourceTable) != "" {
		query += " AND " + s.store.Identifier("source_table") + " = " + s.store.Placeholder(2)
		args = append(args, strings.TrimSpace(sourceTable))
	}
	query += " ORDER BY " + s.store.Identifier("archived_at") + " DESC LIMIT " + fmt.Sprintf("%d", limit)
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
	columns := []string{"id", "workspace_id", "event", "resource_id", "policy_key", "created_at", "payload_json"}
	_, err = s.db.ExecContext(ctx, s.store.InsertStatement("lifecycle_audit_evidence", columns), evidence.ID, evidence.WorkspaceID, evidence.Event, evidence.ResourceID, evidence.PolicyKey, lifecycleTime(evidence.CreatedAt), string(payload))
	return err
}
