package publicresource

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	publicresourcemodel "github.com/domainry/domainry-runtime/runtime/domain/publicresource/model"
	publicresourcerepository "github.com/domainry/domainry-runtime/runtime/domain/publicresource/repository"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

type Store struct{ runtime *database.RuntimeStore }

func NewStore(store *database.RuntimeStore) *Store { return &Store{runtime: store} }

func (store *Store) Find(ctx context.Context, object definitionmodel.ObjectSchema, resource definitionmodel.ObjectPublicResource, accessKey string) (publicresourcemodel.Projection, bool, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil {
		return publicresourcemodel.Projection{}, false, fmt.Errorf("public resource store is unavailable")
	}
	accessKey = strings.TrimSpace(accessKey)
	if accessKey == "" {
		return publicresourcemodel.Projection{}, false, nil
	}
	columns := []string{"workspace_id", "id"}
	seen := map[string]bool{"workspace_id": true, "id": true}
	for _, fieldKey := range resource.Fields {
		fieldKey = strings.TrimSpace(fieldKey)
		if fieldKey != "" && !seen[fieldKey] {
			seen[fieldKey] = true
			columns = append(columns, fieldKey)
		}
	}
	for _, file := range resource.Files {
		fieldKey := strings.TrimSpace(file.FieldKey)
		if fieldKey != "" && !seen[fieldKey] {
			seen[fieldKey] = true
			columns = append(columns, fieldKey)
		}
	}
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), object.Key).
		Columns(columns...).
		Where(query.And(
			query.Equal(strings.TrimSpace(resource.AccessKeyField), accessKey),
			query.Equal(strings.TrimSpace(resource.StateField), strings.TrimSpace(resource.ActiveState)),
			query.Equal("deleted", false),
		)).
		OrderBy(query.Ascending("workspace_id"), query.Ascending("id")).
		Limit(2).
		Build()
	if err != nil {
		return publicresourcemodel.Projection{}, false, fmt.Errorf("build public resource lookup: %w", err)
	}
	rows, err := store.runtime.DB().QueryContext(ctx, statement, arguments...)
	if err != nil {
		return publicresourcemodel.Projection{}, false, fmt.Errorf("query public resource: %w", err)
	}
	defer rows.Close()
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	results := make([]publicresourcemodel.Projection, 0, 2)
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return publicresourcemodel.Projection{}, false, fmt.Errorf("scan public resource: %w", err)
		}
		projection := publicresourcemodel.Projection{Data: map[string]any{}, FileRecords: map[string]string{}}
		for index, column := range columns {
			value := recordpersistence.NormalizeRecordDatabaseValue(store.runtime.RuntimeEngine, fields[column], values[index])
			switch column {
			case "workspace_id":
				projection.WorkspaceID = strings.TrimSpace(fmt.Sprint(value))
			case "id":
				projection.RecordID = strings.TrimSpace(fmt.Sprint(value))
			default:
				if value != nil {
					projection.Data[column] = value
				}
			}
		}
		for _, file := range resource.Files {
			fieldKey := strings.TrimSpace(file.FieldKey)
			if value, exists := projection.Data[fieldKey]; exists {
				if recordID := strings.TrimSpace(fmt.Sprint(value)); recordID != "" {
					projection.FileRecords[fieldKey] = recordID
				}
				delete(projection.Data, fieldKey)
			}
		}
		results = append(results, projection)
	}
	if err := rows.Err(); err != nil {
		return publicresourcemodel.Projection{}, false, fmt.Errorf("read public resource: %w", err)
	}
	if len(results) == 0 {
		return publicresourcemodel.Projection{}, false, nil
	}
	if len(results) != 1 || results[0].WorkspaceID == "" || results[0].RecordID == "" {
		return publicresourcemodel.Projection{}, false, fmt.Errorf("public resource access key is ambiguous")
	}
	return results[0], true, nil
}

var _ publicresourcerepository.Repository = (*Store)(nil)
