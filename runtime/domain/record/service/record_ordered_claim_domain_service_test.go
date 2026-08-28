package service

import (
	"context"
	"errors"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type orderedClaimRepositoryStub struct {
	recordrepository.RecordRepository
	page       recordmodel.RecordPageResult
	listErr    error
	updateErr  error
	claimAfter int
	attempts   int
	query      recordmodel.RecordListQuery
}

func (stub *orderedClaimRepositoryStub) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	stub.query = query
	return stub.page, stub.listErr
}

func (stub *orderedClaimRepositoryStub) UpdateRecordWhere(_ context.Context, _ string, _ definitionmodel.ObjectSchema, _ recordmodel.Record, _ map[string]any) (bool, error) {
	stub.attempts++
	if stub.updateErr != nil {
		return false, stub.updateErr
	}
	return stub.claimAfter > 0 && stub.attempts >= stub.claimAfter, nil
}

func TestRecordOrderedClaimCoversGenericContractAndRepositoryEdges(t *testing.T) {
	valid := RecordOrderedClaimRequest{
		WorkspaceID: "workspace", Object: definitionmodel.ObjectSchema{Key: "work_item"},
		StatusField: "status", EligibleStatuses: []string{" ready ", " "}, ClaimedStatus: "processing",
		ClaimPatch: map[string]any{" owner ": "user", " ": "ignored"}, Now: time.Date(2026, 7, 22, 1, 2, 3, 0, time.UTC),
	}
	for index, mutate := range []func(*RecordOrderedClaimRequest){
		func(*RecordOrderedClaimRequest) {},
		func(value *RecordOrderedClaimRequest) { value.WorkspaceID = "" },
		func(value *RecordOrderedClaimRequest) { value.Object.Key = "" },
		func(value *RecordOrderedClaimRequest) { value.StatusField = "" },
		func(value *RecordOrderedClaimRequest) { value.ClaimedStatus = "" },
		func(value *RecordOrderedClaimRequest) { value.EligibleStatuses = nil },
	} {
		request := valid
		mutate(&request)
		var repository recordrepository.RecordRepository = &orderedClaimRepositoryStub{}
		if index == 0 {
			repository = nil
		}
		if _, _, err := RecordClaimFirstEligible(t.Context(), repository, request); err == nil {
			t.Fatalf("accepted incomplete request=%#v repository=%T", request, repository)
		}
	}
	emptyEligible := valid
	emptyEligible.EligibleStatuses = []string{" "}
	if _, _, err := RecordClaimFirstEligible(t.Context(), &orderedClaimRepositoryStub{}, emptyEligible); err == nil {
		t.Fatal("blank eligible statuses accepted")
	}

	listErr := errors.New("list failed")
	if _, _, err := RecordClaimFirstEligible(t.Context(), &orderedClaimRepositoryStub{listErr: listErr}, valid); !errors.Is(err, listErr) {
		t.Fatalf("list err=%v", err)
	}
	empty := &orderedClaimRepositoryStub{}
	if _, claimed, err := RecordClaimFirstEligible(t.Context(), empty, valid); err != nil || claimed {
		t.Fatalf("empty claimed=%v err=%v", claimed, err)
	}

	page := recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "item-1", Data: map[string]any{"status": "ready", "priority": 10}}}}
	updateErr := errors.New("update failed")
	if _, _, err := RecordClaimFirstEligible(t.Context(), &orderedClaimRepositoryStub{page: page, updateErr: updateErr}, valid); !errors.Is(err, updateErr) {
		t.Fatalf("update err=%v", err)
	}
	success := &orderedClaimRepositoryStub{page: page, claimAfter: 2}
	claimedRecord, claimed, err := RecordClaimFirstEligible(t.Context(), success, valid)
	if err != nil || !claimed || success.attempts != 2 || claimedRecord.Data["status"] != "processing" || claimedRecord.Data["owner"] != "user" || claimedRecord.Data[""] != nil {
		t.Fatalf("record=%#v claimed=%v attempts=%d err=%v", claimedRecord, claimed, success.attempts, err)
	}
	if success.query.Page != 1 || success.query.PageSize != 1 || success.query.Scope != "all_records" || len(success.query.Sort) != 4 {
		t.Fatalf("query=%#v", success.query)
	}

	contention := &orderedClaimRepositoryStub{page: page}
	zeroTime := valid
	zeroTime.Now = time.Time{}
	zeroTime.Query.Filters = map[string]any{"tenant": "a"}
	if _, claimed, err := RecordClaimFirstEligible(t.Context(), contention, zeroTime); err == nil || claimed || contention.attempts != 32 {
		t.Fatalf("claimed=%v attempts=%d err=%v", claimed, contention.attempts, err)
	}
	if contention.query.Filters["tenant"] != "a" {
		t.Fatalf("existing filters=%#v", contention.query.Filters)
	}
	if valueOrDefaultString("value", "fallback") != "value" || valueOrDefaultString("", "fallback") != "fallback" {
		t.Fatal("default string contract changed")
	}
}
