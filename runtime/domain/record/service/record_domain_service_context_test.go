// Record domain service context tests.
package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type applicationContextRepository struct {
	recordrepository.RecordRepository
	observed   chan error
	requestIDs chan string
}

func (r applicationContextRepository) GetRecord(ctx context.Context, _ string, _ definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
	if r.requestIDs != nil {
		r.requestIDs <- requestcontext.RequestID(ctx)
	}
	<-ctx.Done()
	r.observed <- ctx.Err()
	return recordmodel.Record{}, false, ctx.Err()
}

func (r applicationContextRepository) ListRecords(ctx context.Context, _ string, _ definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if r.requestIDs != nil {
		r.requestIDs <- requestcontext.RequestID(ctx)
	}
	<-ctx.Done()
	r.observed <- ctx.Err()
	return recordmodel.RecordPageResult{}, ctx.Err()
}

type applicationContextPolicy struct{ object definitionmodel.ObjectSchema }

type applicationLifecycleIdentityProjection struct{ identityProjectionNoop }

func (p applicationContextPolicy) ObjectForAction(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
	return p.object, nil
}

func (applicationContextPolicy) EnsureReportSnapshotAccess(definitionmodel.ObjectSchema, string, principalmodel.Principal) error {
	return nil
}

func (applicationContextPolicy) NormalizeListQuery(_ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}

func (applicationContextPolicy) CanAccessRecord(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}

func TestRecordDomainServicePropagatesRequestContext(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "account", Name: "Account"}
	observed := make(chan error, 1)
	requestIDs := make(chan string, 1)
	repository := applicationContextRepository{observed: observed, requestIDs: requestIDs}
	service := NewRecordDomainService(RecordDomainServiceDependencies{
		Repository: repository,
		Reader: NewRecordReadDomainService(RecordReadDependencies{
			Repository: repository,
			Policy:     applicationContextPolicy{object: object},
		}),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ctx = requestcontext.WithRequestID(ctx, "req-record-query")

	if _, err := service.GetRecord(ctx, object.Key, "account_1", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); err == nil {
		t.Fatal("expected cancelled repository query to fail")
	}
	select {
	case err := <-observed:
		if err != context.DeadlineExceeded {
			t.Fatalf("repository observed %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("repository did not observe request deadline")
	}
	if got := <-requestIDs; got != "req-record-query" {
		t.Fatalf("repository observed request id %q", got)
	}
}

func TestRecordDomainServiceRetainsConstructorDependencies(t *testing.T) {
	repository := &applicationContextRepository{}
	projection := applicationLifecycleIdentityProjection{}
	service := NewRecordDomainService(RecordDomainServiceDependencies{Repository: repository, IdentityProjection: projection})
	if service.Repository() != repository || service.IdentityProjection() != projection {
		t.Fatal("constructor did not retain owner dependencies")
	}
}
