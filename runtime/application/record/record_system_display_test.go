package record

import (
	"context"
	"errors"
	"reflect"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

type recordSystemDisplayProjection struct {
	recordCompositionIdentityProjection
	query  identitysdk.DisplayNameQuery
	result identitysdk.DisplayNameResult
	err    error
}

func (p *recordSystemDisplayProjection) ResolveDisplayNames(_ context.Context, query identitysdk.DisplayNameQuery) (identitysdk.DisplayNameResult, error) {
	p.query = query
	return p.result, p.err
}

func TestRecordSystemDisplayNamesAreBatchResolvedWithoutReplacingIDs(t *testing.T) {
	projection := &recordSystemDisplayProjection{result: identitysdk.DisplayNameResult{
		Users: []identitysdk.DisplayName{
			{ID: "creator-1", Name: "Creator"},
			{ID: "owner-1", Name: "Owner"},
			{ID: "updater-1", Name: "Updater"},
		},
		OrganizationUnits: []identitysdk.DisplayName{{ID: "org-1", Name: "East Region"}},
	}}
	service := &RecordApplicationService{RecordDomainService: recordservice.NewRecordDomainService(recordservice.RecordDomainServiceDependencies{IdentityProjection: projection})}
	records := []recordmodel.Record{
		{ID: "one", CreateBy: "creator-1", UpdateBy: "updater-1", OwnerUserID: "owner-1", OwnerOrgID: "org-1"},
		{ID: "two", CreateBy: "creator-1", OwnerUserID: "owner-1"},
	}

	service.resolveRecordSystemDisplayNames(t.Context(), records)

	if !reflect.DeepEqual(projection.query.UserIDs, []string{"creator-1", "owner-1", "updater-1"}) || !reflect.DeepEqual(projection.query.OrganizationUnitIDs, []string{"org-1"}) {
		t.Fatalf("batch query = %#v", projection.query)
	}
	if records[0].CreateBy != "creator-1" || records[0].OwnerUserID != "owner-1" || records[0].OwnerOrgID != "org-1" {
		t.Fatalf("stable IDs changed: %#v", records[0])
	}
	if records[0].CreateByName != "Creator" || records[0].UpdateByName != "Updater" || records[0].OwnerUserName != "Owner" || records[0].OwnerOrgName != "East Region" {
		t.Fatalf("display names = %#v", records[0])
	}
}

func TestRecordSystemDisplayNameFailureLeavesReadableIDs(t *testing.T) {
	projection := &recordSystemDisplayProjection{err: errors.New("identity unavailable")}
	service := &RecordApplicationService{RecordDomainService: recordservice.NewRecordDomainService(recordservice.RecordDomainServiceDependencies{IdentityProjection: projection})}
	records := []recordmodel.Record{{ID: "one", OwnerUserID: "owner-1"}}

	service.resolveRecordSystemDisplayNames(t.Context(), records)

	if records[0].OwnerUserID != "owner-1" || records[0].OwnerUserName != "" {
		t.Fatalf("record after projection failure = %#v", records[0])
	}
}
