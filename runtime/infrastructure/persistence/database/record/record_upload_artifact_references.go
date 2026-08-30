package record

import (
	"context"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// UploadArtifactReferences owns knowledge of dynamic record tables and fields.
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
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, objectKey, workspaceID).Projections(ormbuilder.Project(ormbuilder.CountAll())).
		Where(ormbuilder.Or(ormbuilder.Equal(fieldKey, reference), ormbuilder.Like(fieldKey, "%\""+reference+"\"%"))).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build upload artifact reference query: %w", buildErr)
	}
	var count int
	if err := r.store.DB().QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
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
