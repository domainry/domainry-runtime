package repository

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

// RecordRepository is the ctx-first record aggregate contract.
type RecordRepository interface {
	ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
	GetRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error)
	InsertRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record) error
	UpdateRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record) error
	UpdateRecordWhere(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error)
	DeleteRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) error
	CommitRecordMutation(ctx context.Context, workspaceID string, commit transactionmodel.RecordMutationCommit) error
	CommitRecordMutationBatch(ctx context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) error
	UniqueExists(ctx context.Context, workspaceID, objectKey, fieldKey, currentID string, value any) (bool, error)
}

// RecordIdentitySeedRepository persists Identity-owned object rows materialized
// from a manifest.
type RecordIdentitySeedRepository interface {
	GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
	InsertRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error
}

// RecordBusinessSeedRepository persists generated business seed rows after the
// Application layer has resolved ordering and references.
type RecordBusinessSeedRepository interface {
	ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
	InsertRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error
}
