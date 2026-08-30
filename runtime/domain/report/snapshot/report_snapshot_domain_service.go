package snapshot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type ReportSnapshotRefresh struct {
	Snapshot       reportmodel.ReportSnapshot
	TerminalStatus string
	ErrorCode      string
}

func reportAppError(kind apperror.ErrorKind, code string, err error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}

// ReportSnapshotQuery is the only report execution capability Snapshot needs.
// It prevents refresh lifecycle code from depending on Dataset or Object SQL
// implementation details.
type ReportSnapshotQuery interface {
	ExecuteReportSnapshot(context.Context, reportmodel.ReportSchema, principalmodel.Principal) (reportmodel.ReportSummary, error)
	ReadReportSnapshotSourceVersion(context.Context, reportmodel.ReportSchema, principalmodel.Principal) (reportmodel.ReportSnapshotSourceVersion, error)
}

type ReportSnapshotDependencies struct {
	Reports func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema
	Store   reportcontract.ReportSnapshotStore
	Query   ReportSnapshotQuery
	Clock   func() time.Time
}

// ReportSnapshotDomainService owns materialized-report refresh consistency and
// snapshot lifecycle. Query evaluation is delegated through ReportSnapshotQuery.
type ReportSnapshotDomainService struct{ dependencies ReportSnapshotDependencies }

func NewReportSnapshotDomainService(dependencies ReportSnapshotDependencies) *ReportSnapshotDomainService {
	if dependencies.Clock == nil {
		dependencies.Clock = time.Now
	}
	return &ReportSnapshotDomainService{dependencies: dependencies}
}

func (s *ReportSnapshotDomainService) reportForPrincipal(ctx context.Context, reportKey string, principal principalmodel.Principal) (reportmodel.ReportSchema, bool) {
	if s == nil || s.dependencies.Reports == nil {
		return reportmodel.ReportSchema{}, false
	}
	reportKey = strings.TrimSpace(reportKey)
	for _, report := range s.dependencies.Reports(ctx, principal) {
		if report.Key == reportKey {
			return report, true
		}
	}
	return reportmodel.ReportSchema{}, false
}

func (s *ReportSnapshotDomainService) RefreshSnapshot(ctx context.Context, reportKey, idempotencyKey string, principal principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	refresh, err := s.PrepareSnapshotRefresh(ctx, reportKey, idempotencyKey, principal)
	if refresh.TerminalStatus == "failed" {
		_ = s.CommitSnapshotRefresh(ctx, refresh)
		return reportmodel.ReportSnapshot{}, err
	}
	if err != nil {
		return reportmodel.ReportSnapshot{}, err
	}
	if refresh.TerminalStatus == "succeeded" {
		if err := s.CommitSnapshotRefresh(ctx, refresh); err != nil {
			return reportmodel.ReportSnapshot{}, reportAppError(apperror.KindConflict, "backend.report.snapshot_fenced", err)
		}
	}
	return refresh.Snapshot, nil
}

// PrepareSnapshotRefresh evaluates a materialized report but deliberately
// leaves the terminal snapshot transition uncommitted. The application layer
// can therefore coordinate that owner write with a Notification Event in one
// transaction without importing Notification into the Report domain.
func (s *ReportSnapshotDomainService) PrepareSnapshotRefresh(ctx context.Context, reportKey, idempotencyKey string, principal principalmodel.Principal) (ReportSnapshotRefresh, error) {
	report, ok := s.reportForPrincipal(ctx, reportKey, principal)
	if !ok {
		return ReportSnapshotRefresh{}, reportAppError(apperror.KindNotFound, "backend.report.not_found", nil)
	}
	if report.Materialization == nil {
		return ReportSnapshotRefresh{}, reportAppError(apperror.KindBadRequest, "backend.report.materialization_not_enabled", nil)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return ReportSnapshotRefresh{}, reportAppError(apperror.KindBadRequest, "backend.idempotency.key_required", nil)
	}
	if s.dependencies.Store == nil || s.dependencies.Query == nil {
		return ReportSnapshotRefresh{}, reportAppError(apperror.KindInternal, "backend.report.snapshot_unavailable", nil)
	}
	now := s.dependencies.Clock().UTC()
	leaseOwner, err := newReportSnapshotLeaseOwner()
	if err != nil {
		return ReportSnapshotRefresh{}, reportAppError(apperror.KindInternal, "backend.report.snapshot_claim_failed", err)
	}
	scopeHash, err := reportSnapshotAccessScopeHash(principal)
	if err != nil {
		return ReportSnapshotRefresh{}, reportAppError(apperror.KindInternal, "backend.report.snapshot_scope_invalid", err)
	}
	claim, err := s.dependencies.Store.BeginReportSnapshot(ctx, reportcontract.ReportSnapshotBeginRequest{
		WorkspaceID: principal.WorkspaceID, ReportKey: report.Key, AccessScopeHash: scopeHash,
		IdempotencyKey: strings.TrimSpace(idempotencyKey), StartedAt: now.Format(time.RFC3339Nano),
		LeaseOwner: leaseOwner, LeaseExpiresAt: now.Add(2 * time.Minute).Format(time.RFC3339Nano),
	})
	if err != nil {
		return ReportSnapshotRefresh{}, reportAppError(apperror.KindInternal, "backend.report.snapshot_begin_failed", err)
	}
	if !claim.Acquired() {
		return ReportSnapshotRefresh{Snapshot: claim.Snapshot}, nil
	}
	snapshot := claim.Snapshot
	retries := report.Materialization.ConsistencyRetries
	if retries <= 0 {
		retries = 3
	}
	var summary reportmodel.ReportSummary
	var version reportmodel.ReportSnapshotSourceVersion
	stable := false
	sourceChanged := false
	for attempt := 0; attempt < retries; attempt++ {
		before, readErr := s.dependencies.Query.ReadReportSnapshotSourceVersion(ctx, report, principal)
		if readErr != nil {
			err = readErr
			break
		}
		summary, err = s.dependencies.Query.ExecuteReportSnapshot(ctx, report, principal)
		if err != nil {
			break
		}
		version, err = s.dependencies.Query.ReadReportSnapshotSourceVersion(ctx, report, principal)
		if err != nil {
			break
		}
		if reportSnapshotVersionsEqual(before, version) {
			stable = true
			break
		}
		sourceChanged = true
	}
	if err == nil && !stable {
		err = fmt.Errorf("source versions changed during every refresh attempt")
	}
	if err != nil {
		code := "backend.report.snapshot_refresh_failed"
		if sourceChanged {
			code = "backend.report.snapshot_source_changed"
		}
		snapshot.Status, snapshot.ErrorCode = "failed", code
		return ReportSnapshotRefresh{Snapshot: snapshot, TerminalStatus: "failed", ErrorCode: code}, reportAppError(apperror.KindConflict, code, err)
	}
	refreshedAt := s.dependencies.Clock().UTC()
	summary.ExecutionMode = "snapshot"
	summary.Snapshot = nil
	snapshot.Status, snapshot.Summary = "succeeded", summary
	snapshot.Watermark, snapshot.SourceVersions = version.Watermark, version.SourceVersions
	snapshot.RefreshedAt = refreshedAt.Format(time.RFC3339Nano)
	return ReportSnapshotRefresh{Snapshot: snapshot, TerminalStatus: "succeeded"}, nil
}

func (s *ReportSnapshotDomainService) CommitSnapshotRefresh(ctx context.Context, refresh ReportSnapshotRefresh) error {
	switch refresh.TerminalStatus {
	case "succeeded":
		return s.dependencies.Store.CompleteReportSnapshot(ctx, reportcontract.ReportSnapshotCompleteRequest{Snapshot: refresh.Snapshot, ExpectedStatus: "refreshing", LeaseOwner: refresh.Snapshot.LeaseOwner, FencingToken: refresh.Snapshot.FencingToken})
	case "failed":
		return s.dependencies.Store.FailReportSnapshot(ctx, reportcontract.ReportSnapshotFailRequest{WorkspaceID: refresh.Snapshot.WorkspaceID, ID: refresh.Snapshot.ID, ExpectedStatus: "refreshing", ErrorCode: refresh.ErrorCode, LeaseOwner: refresh.Snapshot.LeaseOwner, FencingToken: refresh.Snapshot.FencingToken})
	default:
		return nil
	}
}

func (s *ReportSnapshotDomainService) ReadSnapshot(ctx context.Context, report reportmodel.ReportSchema, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if report.Materialization == nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.materialization_not_enabled", nil)
	}
	if s.dependencies.Store == nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.snapshot_unavailable", nil)
	}
	scopeHash, err := reportSnapshotAccessScopeHash(principal)
	if err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.snapshot_scope_invalid", err)
	}
	snapshot, ok, err := s.dependencies.Store.LatestReportSnapshot(ctx, principal.WorkspaceID, report.Key, scopeHash)
	if err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.snapshot_read_failed", err)
	}
	if !ok {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindNotFound, "backend.report.snapshot_not_found", nil)
	}
	refreshedAt, err := time.Parse(time.RFC3339Nano, snapshot.RefreshedAt)
	if err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.snapshot_invalid", err)
	}
	lag := s.dependencies.Clock().UTC().Sub(refreshedAt)
	if lag < 0 {
		lag = 0
	}
	summary := snapshot.Summary
	summary.ExecutionMode = "snapshot"
	summary.Snapshot = &reportmodel.ReportSnapshotFreshness{
		SnapshotID: snapshot.ID, Watermark: snapshot.Watermark, SourceVersions: snapshot.SourceVersions,
		RefreshedAt: snapshot.RefreshedAt, LagSeconds: int64(lag / time.Second),
		Stale: report.Materialization.MaximumLagSeconds > 0 && lag > time.Duration(report.Materialization.MaximumLagSeconds)*time.Second,
	}
	return summary, nil
}

func reportSnapshotAccessScopeHash(principal principalmodel.Principal) (string, error) {
	return ReportAccessScopeHash(principal)
}

// ReportAccessScopeHash binds every principal fact that can affect report RLS,
// CLS, role permissions, Surface visibility, or authorization revision.
func ReportAccessScopeHash(principal principalmodel.Principal) (string, error) {
	principal = canonicalReportAccessPrincipal(principal)
	payload := struct {
		WorkspaceID           string
		UserID                string
		DepartmentID          string
		DepartmentPath        string
		ReportingPath         string
		ReportingUserIDs      []string
		TeamIDs               []string
		StoreIDs              []string
		TerritoryIDs          []string
		WarehouseIDs          []string
		RoleKey               string
		BusinessProfiles      []profilebindingmodel.Reference
		ActiveBusinessProfile *profilebindingmodel.Reference
		BusinessClaims        map[string]profilebindingmodel.ClaimValue
		SurfaceKey            string
		AuthorizationRevision string
	}{principal.WorkspaceID, principal.UserID, principal.DepartmentID, principal.DepartmentPath, principal.ReportingPath, principal.ReportingUserIDs, principal.OrganizationScopes.TeamIDs, principal.OrganizationScopes.StoreIDs, principal.OrganizationScopes.TerritoryIDs, principal.OrganizationScopes.WarehouseIDs, principal.RoleKey, principal.BusinessProfiles, principal.ActiveBusinessProfile, principal.BusinessClaims, principal.SurfaceKey, principal.AuthorizationRevision}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalReportAccessPrincipal normalizes only set-like authorization facts.
// It does not remove or weaken any fact bound by ReportAccessScopeHash: a real
// permission, role, organization, business-profile or revision change still
// changes the hash. The normalization only prevents transport/resolver slice
// ordering and nil-vs-empty representation from looking like scope changes.
func canonicalReportAccessPrincipal(principal principalmodel.Principal) principalmodel.Principal {
	principal.ReportingUserIDs = canonicalReportAccessStrings(principal.ReportingUserIDs)
	principal.OrganizationScopes.TeamIDs = canonicalReportAccessStrings(principal.OrganizationScopes.TeamIDs)
	principal.OrganizationScopes.StoreIDs = canonicalReportAccessStrings(principal.OrganizationScopes.StoreIDs)
	principal.OrganizationScopes.TerritoryIDs = canonicalReportAccessStrings(principal.OrganizationScopes.TerritoryIDs)
	principal.OrganizationScopes.WarehouseIDs = canonicalReportAccessStrings(principal.OrganizationScopes.WarehouseIDs)

	profiles := append([]profilebindingmodel.Reference(nil), principal.BusinessProfiles...)
	for index := range profiles {
		profiles[index].SurfaceKeys = canonicalReportAccessStrings(profiles[index].SurfaceKeys)
	}
	principal.BusinessProfiles = canonicalReportAccessSlice(profiles)
	if principal.ActiveBusinessProfile != nil {
		active := *principal.ActiveBusinessProfile
		active.SurfaceKeys = canonicalReportAccessStrings(active.SurfaceKeys)
		principal.ActiveBusinessProfile = &active
	}
	return principal
}

func canonicalReportAccessStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func canonicalReportAccessSlice[T any](values []T) []T {
	out := append([]T{}, values...)
	sort.Slice(out, func(left, right int) bool {
		leftJSON, _ := json.Marshal(out[left])
		rightJSON, _ := json.Marshal(out[right])
		return string(leftJSON) < string(rightJSON)
	})
	return out
}

func reportSnapshotVersionsEqual(left, right reportmodel.ReportSnapshotSourceVersion) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func newReportSnapshotLeaseOwner() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "report-snapshot:" + hex.EncodeToString(value), nil
}
