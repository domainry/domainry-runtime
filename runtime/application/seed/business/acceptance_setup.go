package businessseed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	appschemaprojection "github.com/domainry/domainry-runtime/runtime/domain/appschema/projection"
	businessseedcontract "github.com/domainry/domainry-runtime/runtime/domain/businessseed/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	auditbinding "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
)

const (
	maximumAcceptanceFixtureBytes   = 1 << 20
	maximumAcceptanceBusinessRows   = 32
	maximumAcceptanceRelativeAge    = 8760 * time.Hour
	acceptanceFixtureAuditEvent     = "runtime.acceptance_fixture.record_created"
	acceptanceFixtureSystemActor    = "runtime-acceptance-fixture"
	acceptanceFixtureRecordIDPrefix = "acceptance_"
)

var acceptanceFixtureKey = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

type acceptanceFixtureStore interface {
	GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
	CommitRecordMutation(context.Context, string, transactionmodel.RecordMutationCommit) error
}

type AcceptanceFixtureIdentityReference struct {
	ObjectKey string
	RecordID  string
}

type acceptanceFixtureEnvelope struct {
	ContractVersion string                            `json:"contract_version"`
	WorkspaceCode   string                            `json:"workspace_code"`
	Organizations   []acceptanceFixtureOrganization   `json:"organizations"`
	Actors          []acceptanceFixtureActor          `json:"actors"`
	BusinessRecords []acceptanceFixtureBusinessRecord `json:"business_records"`
}

type acceptanceFixtureOrganization struct {
	ID string `json:"id"`
}

type acceptanceFixtureActor struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
}

type acceptanceFixtureBusinessRecord struct {
	FixtureKey          string            `json:"fixture_key"`
	ObjectKey           string            `json:"object_key"`
	RecordID            string            `json:"record_id"`
	Data                map[string]any    `json:"data"`
	OwnerActorID        string            `json:"owner_actor_id"`
	OwnerOrganizationID string            `json:"owner_organization_id"`
	RelativeTimes       map[string]string `json:"relative_times"`
}

// SyncRuntimeAcceptanceFixtures materializes bounded acceptance-only business
// rows during startup. The caller owns the environment/driver gate; this
// function owns Workspace, schema, actor, organization, and relative-time safety.
func SyncRuntimeAcceptanceFixtures(ctx context.Context, store acceptanceFixtureStore, manifest manifestmodel.ManifestSchema, workspaceID, workspaceCode string, identityReferences []AcceptanceFixtureIdentityReference, raw string, now time.Time) error {
	workspaceID = strings.TrimSpace(workspaceID)
	workspaceCode = strings.TrimSpace(workspaceCode)
	if store == nil || workspaceID == "" || workspaceCode == "" {
		return fmt.Errorf("Runtime acceptance fixtures require a committed Workspace installation")
	}
	if len(raw) == 0 || len(raw) > maximumAcceptanceFixtureBytes {
		return fmt.Errorf("Runtime acceptance fixture payload size is invalid")
	}
	var envelope acceptanceFixtureEnvelope
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("decode Runtime acceptance fixtures: %w", err)
	}
	if err := requireAcceptanceFixtureEOF(decoder); err != nil {
		return fmt.Errorf("decode Runtime acceptance fixtures: %w", err)
	}
	if strings.TrimSpace(envelope.ContractVersion) != businessseedcontract.RuntimeAcceptanceFixtureContractVersion {
		return fmt.Errorf("Runtime acceptance fixture contract_version is unsupported")
	}
	if strings.TrimSpace(envelope.WorkspaceCode) != workspaceCode {
		return fmt.Errorf("Runtime acceptance fixture workspace_code does not match the committed installation")
	}
	if len(envelope.BusinessRecords) == 0 || len(envelope.BusinessRecords) > maximumAcceptanceBusinessRows {
		return fmt.Errorf("Runtime acceptance fixture business_records count is invalid")
	}

	organizations := map[string]bool{}
	for _, organization := range envelope.Organizations {
		id := strings.TrimSpace(organization.ID)
		if id == "" || organizations[id] {
			return fmt.Errorf("Runtime acceptance fixture organization id is empty or duplicated")
		}
		organizations[id] = true
	}
	actors := map[string]string{}
	for _, actor := range envelope.Actors {
		id := strings.TrimSpace(actor.ID)
		if id == "" {
			return fmt.Errorf("Runtime acceptance fixture actor id is empty")
		}
		if _, found := actors[id]; found {
			return fmt.Errorf("Runtime acceptance fixture actor id is duplicated")
		}
		actors[id] = strings.TrimSpace(actor.OrganizationID)
	}
	installedActors, installedOrganizations := map[string]bool{}, map[string]bool{}
	for _, reference := range identityReferences {
		switch strings.TrimSpace(reference.ObjectKey) {
		case "identity_user":
			installedActors[strings.TrimSpace(reference.RecordID)] = true
		case "identity_organization_unit":
			installedOrganizations[strings.TrimSpace(reference.RecordID)] = true
		}
	}
	objects := map[string]definitionmodel.ObjectSchema{}
	for _, object := range appschemaprojection.ApplicationSchemaEnrichObjectsWithFieldValueDomains(manifest.Objects, manifest.Dictionaries) {
		objects[strings.TrimSpace(object.Key)] = object
	}
	seenFixtureKeys, seenRecordIDs := map[string]bool{}, map[string]bool{}
	now = now.UTC()
	if now.IsZero() {
		return fmt.Errorf("Runtime acceptance fixture clock is required")
	}
	for _, fixture := range envelope.BusinessRecords {
		if err := syncRuntimeAcceptanceFixture(ctx, store, workspaceID, objects, organizations, actors, installedActors, installedOrganizations, fixture, now, seenFixtureKeys, seenRecordIDs); err != nil {
			return err
		}
	}
	return nil
}

func requireAcceptanceFixtureEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	} else if err != io.EOF {
		return err
	}
	return nil
}

func syncRuntimeAcceptanceFixture(ctx context.Context, store acceptanceFixtureStore, workspaceID string, objects map[string]definitionmodel.ObjectSchema, organizations map[string]bool, actors map[string]string, installedActors, installedOrganizations map[string]bool, fixture acceptanceFixtureBusinessRecord, now time.Time, seenFixtureKeys, seenRecordIDs map[string]bool) error {
	fixtureKey := strings.TrimSpace(fixture.FixtureKey)
	objectKey := strings.TrimSpace(fixture.ObjectKey)
	recordID := strings.TrimSpace(fixture.RecordID)
	actorID := strings.TrimSpace(fixture.OwnerActorID)
	organizationID := strings.TrimSpace(fixture.OwnerOrganizationID)
	if !acceptanceFixtureKey.MatchString(fixtureKey) || seenFixtureKeys[fixtureKey] {
		return fmt.Errorf("Runtime acceptance fixture_key is invalid or duplicated")
	}
	seenFixtureKeys[fixtureKey] = true
	if !strings.HasPrefix(recordID, acceptanceFixtureRecordIDPrefix) || !acceptanceFixtureKey.MatchString(recordID) || seenRecordIDs[recordID] {
		return fmt.Errorf("Runtime acceptance fixture %s record_id is invalid or duplicated", fixtureKey)
	}
	seenRecordIDs[recordID] = true
	object, ok := objects[objectKey]
	if !ok {
		return fmt.Errorf("Runtime acceptance fixture %s references an unknown object", fixtureKey)
	}
	if transactionmodel.MutationObjectWritePolicy(object) != transactionmodel.ObjectWritePolicyActionOnly {
		return fmt.Errorf("Runtime acceptance fixture %s requires an action_only object", fixtureKey)
	}
	actorOrganizationID, ok := actors[actorID]
	if !ok || actorID == "" {
		return fmt.Errorf("Runtime acceptance fixture %s owner_actor_id is not declared", fixtureKey)
	}
	if !installedActors[actorID] {
		return fmt.Errorf("Runtime acceptance fixture %s owner_actor_id is not in the committed Identity fixture graph", fixtureKey)
	}
	if organizationID != actorOrganizationID {
		return fmt.Errorf("Runtime acceptance fixture %s owner organization does not match its actor", fixtureKey)
	}
	if organizationID != "" && !organizations[organizationID] {
		return fmt.Errorf("Runtime acceptance fixture %s owner organization is not declared", fixtureKey)
	}
	if organizationID != "" && !installedOrganizations[organizationID] {
		return fmt.Errorf("Runtime acceptance fixture %s owner organization is not in the committed Identity fixture graph", fixtureKey)
	}
	if len(fixture.RelativeTimes) == 0 {
		return fmt.Errorf("Runtime acceptance fixture %s requires relative_times", fixtureKey)
	}
	data := make(map[string]any, len(fixture.Data)+len(fixture.RelativeTimes))
	for key, value := range fixture.Data {
		data[key] = value
	}
	fields := map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	for fieldKey, rawOffset := range fixture.RelativeTimes {
		field, found := fields[strings.TrimSpace(fieldKey)]
		fieldType := strings.ToLower(strings.TrimSpace(field.Type))
		if !found || (fieldType != "date" && fieldType != "datetime") {
			return fmt.Errorf("Runtime acceptance fixture %s relative time field is not date or datetime", fixtureKey)
		}
		if _, exists := data[field.Key]; exists {
			return fmt.Errorf("Runtime acceptance fixture %s relative time field is duplicated in data", fixtureKey)
		}
		offset, err := time.ParseDuration(strings.TrimSpace(rawOffset))
		if err != nil || offset >= 0 || -offset > maximumAcceptanceRelativeAge {
			return fmt.Errorf("Runtime acceptance fixture %s relative time must be negative and bounded", fixtureKey)
		}
		value := now.Add(offset)
		if fieldType == "date" {
			data[field.Key] = value.Format("2006-01-02")
		} else {
			data[field.Key] = value.Format(time.RFC3339Nano)
		}
	}
	recordpolicy.RecordApplyFieldDefaults(object, data)
	normalized, err := recordvalidation.RecordNormalizeData(object, data, false)
	if err != nil {
		return fmt.Errorf("normalize Runtime acceptance fixture %s: %w", fixtureKey, err)
	}
	if err := recordvalidation.RecordValidateData(object, normalized, false); err != nil {
		return fmt.Errorf("validate Runtime acceptance fixture %s: %w", fixtureKey, err)
	}
	if existing, found, err := store.GetRecord(ctx, workspaceID, object, recordID); err != nil {
		return fmt.Errorf("inspect Runtime acceptance fixture %s: %w", fixtureKey, err)
	} else if found {
		if existing.ExtInfo["source_kind"] != "runtime_acceptance_fixture" || existing.ExtInfo["fixture_key"] != fixtureKey {
			return fmt.Errorf("Runtime acceptance fixture %s record_id collides with a non-fixture record", fixtureKey)
		}
		return nil
	}
	timestamp := now.Format(time.RFC3339Nano)
	record := recordmodel.Record{
		WorkspaceID: workspaceID, ID: recordID, Data: normalized,
		CreatedAt: timestamp, UpdatedAt: timestamp, CreateBy: acceptanceFixtureSystemActor,
		UpdateBy: acceptanceFixtureSystemActor, OwnerUserID: actorID, OwnerOrgID: organizationID,
		ExtInfo: map[string]any{"source_kind": "runtime_acceptance_fixture", "fixture_key": fixtureKey, "contract_version": businessseedcontract.RuntimeAcceptanceFixtureContractVersion},
	}
	principal := principalmodel.NewSystemPrincipal(acceptanceFixtureSystemActor, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "materialize Runtime acceptance fixture"))
	principal.WorkspaceID = workspaceID
	audit := auditbinding.AuditBuildEvent(ctx, acceptanceFixtureAuditEvent, objectKey, recordID, principal, "Runtime acceptance fixture record created", nil, normalized, map[string]any{"fixture_key": fixtureKey, "contract_version": businessseedcontract.RuntimeAcceptanceFixtureContractVersion})
	audit.CreatedAt = timestamp
	if err := store.CommitRecordMutation(ctx, workspaceID, transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: record, Audit: &audit}); err != nil {
		return fmt.Errorf("commit Runtime acceptance fixture %s: %w", fixtureKey, err)
	}
	return nil
}
