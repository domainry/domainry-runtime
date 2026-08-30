package scheduler

import (
	"context"
	"sync"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type schedulerRepositoryFake struct {
	mu            sync.Mutex
	get           func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
	list          func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
	update        func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error)
	commit        func(context.Context, string, []transactionmodel.RecordMutationCommit) error
	workspaceList func(context.Context, definitionmodel.ObjectSchema, time.Time) ([]string, error)
	getCalls      int
	committed     [][]transactionmodel.RecordMutationCommit
	workspaces    []string
}

func (f *schedulerRepositoryFake) ListDueRecordTimerWorkspaces(ctx context.Context, object definitionmodel.ObjectSchema, now time.Time) ([]string, error) {
	if f.workspaceList != nil {
		return f.workspaceList(ctx, object, now)
	}
	return nil, nil
}

func (f *schedulerRepositoryFake) GetRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
	f.mu.Lock()
	f.getCalls++
	f.workspaces = append(f.workspaces, workspaceID)
	f.mu.Unlock()
	if f.get != nil {
		return f.get(ctx, workspaceID, object, id)
	}
	return recordmodel.Record{}, false, nil
}

func (f *schedulerRepositoryFake) ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if f.list != nil {
		return f.list(ctx, workspaceID, object, query)
	}
	return recordmodel.RecordPageResult{}, nil
}

func (f *schedulerRepositoryFake) UpdateRecordWhere(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error) {
	if f.update != nil {
		return f.update(ctx, workspaceID, object, record, conditions)
	}
	return true, nil
}

func (f *schedulerRepositoryFake) CommitRecordMutationBatch(ctx context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) error {
	f.mu.Lock()
	f.workspaces = append(f.workspaces, workspaceID)
	f.committed = append(f.committed, commits)
	f.mu.Unlock()
	if f.commit != nil {
		return f.commit(ctx, workspaceID, commits)
	}
	return nil
}

func (f *schedulerRepositoryFake) ListSchedulerDefinitions(ctx context.Context) ([]recordmodel.Record, error) {
	page, err := f.ListRecords(ctx, principalmodel.InstallationWorkspaceID, definitionmodel.ObjectSchema{Key: "job_definition"}, recordmodel.RecordListQuery{Page: 1, PageSize: 500})
	return page.Items, err
}

func (f *schedulerRepositoryFake) GetSchedulerDefinition(ctx context.Context, key string) (recordmodel.Record, bool, error) {
	return f.GetRecord(ctx, principalmodel.InstallationWorkspaceID, definitionmodel.ObjectSchema{Key: "job_definition"}, key)
}

func (*schedulerRepositoryFake) ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error) {
	return []SchedulerDefinitionVersion{}, nil
}

type schedulerFixedClock struct{ now time.Time }

func (c schedulerFixedClock) Now() time.Time { return c.now }

func schedulerTestPrincipal(permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, UserID: "operator-1", WorkspaceID: "workspace-a",
	}}, accessfixture.Bundle{Permissions: permissions, RecordScope: "all_records"})
}

func schedulerTestSchema() schedulerSchemaStub {
	return schedulerSchemaStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "record_timer"}, {Key: "record_timer_event"}}}}
}
