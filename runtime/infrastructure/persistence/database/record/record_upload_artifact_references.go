package record

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type UploadArtifactReferences struct {
	store   *database.RuntimeStore
	objects map[string]definitionmodel.ObjectSchema
}

func NewUploadArtifactReferences(store *database.RuntimeStore, objects []definitionmodel.ObjectSchema) *UploadArtifactReferences {
	byKey := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		byKey[strings.TrimSpace(object.Key)] = object
	}
	return &UploadArtifactReferences{store: store, objects: byKey}
}

func (r *UploadArtifactReferences) UploadArtifactReferenced(ctx context.Context, workspaceID, objectKey, fieldKey, filename string) (bool, error) {
	object, ok := r.objects[objectKey]
	if !ok || !recordObjectHasField(object, fieldKey) {
		return false, nil
	}
	reference := "/uploads/" + filename
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, objectKey, workspaceID).Projections(query.Project(query.CountAll())).
		Where(query.Or(query.Equal(fieldKey, reference), query.Like(fieldKey, "%\""+reference+"\"%"))).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build upload artifact reference query: %w", buildErr)
	}
	var count int
	if err := r.store.DB().QueryRowContext(ctx, queryValue, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func recordObjectHasField(object definitionmodel.ObjectSchema, fieldKey string) bool {
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == fieldKey {
			return true
		}
	}
	return false
}
