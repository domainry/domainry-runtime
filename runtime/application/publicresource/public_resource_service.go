package publicresource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	publicresourcemodel "github.com/domainry/domainry-runtime/runtime/domain/publicresource/model"
	publicresourcerepository "github.com/domainry/domainry-runtime/runtime/domain/publicresource/repository"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

const maxPublicResourceFileBytes int64 = 16 << 20

var (
	ErrNotFound         = errors.New("public resource not found")
	ErrRequestInvalid   = errors.New("public resource request invalid")
	publicAccessKeyRE   = regexp.MustCompile(`^[A-Za-z0-9_-]{20,128}$`)
	publicResourceKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

type Service struct {
	repository publicresourcerepository.Repository
	records    recordrepository.RecordRepository
	files      *uploadapplication.FileCapabilityService
	scans      *uploadapplication.FileScanReceiptVerifier
	objects    map[string]definitionmodel.ObjectSchema
	resources  map[string]resourceDefinition
}

type resourceDefinition struct {
	object   definitionmodel.ObjectSchema
	resource definitionmodel.ObjectPublicResource
}

func NewService(objects []definitionmodel.ObjectSchema, repository publicresourcerepository.Repository, records recordrepository.RecordRepository, files *uploadapplication.FileCapabilityService, scans *uploadapplication.FileScanReceiptVerifier) *Service {
	service := &Service{
		repository: repository, records: records, files: files, scans: scans,
		objects: map[string]definitionmodel.ObjectSchema{}, resources: map[string]resourceDefinition{},
	}
	for _, object := range objects {
		objectKey := strings.TrimSpace(object.Key)
		if objectKey == "" {
			continue
		}
		service.objects[objectKey] = object
		for _, resource := range object.PublicResources {
			if key := strings.TrimSpace(resource.Key); key != "" {
				service.resources[key] = resourceDefinition{object: object, resource: resource}
			}
		}
	}
	return service
}

func (service *Service) Get(ctx context.Context, resourceKey, accessKey string) (publicresourcemodel.Resource, error) {
	definition, accessKey, err := service.resolve(resourceKey, accessKey)
	if err != nil {
		return publicresourcemodel.Resource{}, err
	}
	projection, found, err := service.repository.Find(ctx, definition.object, definition.resource, accessKey)
	if err != nil {
		return publicresourcemodel.Resource{}, fmt.Errorf("resolve public resource: %w", err)
	}
	if !found {
		return publicresourcemodel.Resource{}, ErrNotFound
	}
	files := map[string]string{}
	for _, binding := range definition.resource.Files {
		fieldKey := strings.TrimSpace(binding.FieldKey)
		if projection.FileRecords[fieldKey] == "" {
			continue
		}
		files[fieldKey] = "/public-resources/" + url.PathEscape(definition.resource.Key) + "/" + url.PathEscape(accessKey) + "/files/" + url.PathEscape(fieldKey)
	}
	if len(files) == 0 {
		files = nil
	}
	return publicresourcemodel.Resource{ResourceKey: definition.resource.Key, Data: projection.Data, Files: files}, nil
}

func (service *Service) OpenFile(ctx context.Context, resourceKey, accessKey, fileField string) (publicresourcemodel.File, error) {
	definition, accessKey, err := service.resolve(resourceKey, accessKey)
	if err != nil {
		return publicresourcemodel.File{}, err
	}
	fileField = strings.TrimSpace(fileField)
	var binding *definitionmodel.ObjectPublicResourceFile
	for index := range definition.resource.Files {
		if strings.TrimSpace(definition.resource.Files[index].FieldKey) == fileField {
			binding = &definition.resource.Files[index]
			break
		}
	}
	if binding == nil {
		return publicresourcemodel.File{}, ErrNotFound
	}
	projection, found, err := service.repository.Find(ctx, definition.object, definition.resource, accessKey)
	if err != nil {
		return publicresourcemodel.File{}, fmt.Errorf("resolve public resource file owner: %w", err)
	}
	if !found {
		return publicresourcemodel.File{}, ErrNotFound
	}
	fileRecordID := strings.TrimSpace(projection.FileRecords[fileField])
	if fileRecordID == "" {
		return publicresourcemodel.File{}, ErrNotFound
	}
	relation := objectField(definition.object, fileField)
	target := service.objects[strings.TrimSpace(relation.Validation.Target)]
	fileRecord, found, err := service.records.GetRecord(ctx, projection.WorkspaceID, target, fileRecordID)
	if err != nil {
		return publicresourcemodel.File{}, fmt.Errorf("read public resource file metadata: %w", err)
	}
	if !found || fileRecord.Deleted || publicFileDisabled(fileRecord.Data, *binding) {
		return publicresourcemodel.File{}, ErrNotFound
	}
	fileID := stringField(fileRecord.Data, binding.FileIDField)
	filename := stringField(fileRecord.Data, binding.FilenameField)
	mediaType := strings.ToLower(stringField(fileRecord.Data, binding.MediaTypeField))
	contentSHA256 := strings.ToLower(stringField(fileRecord.Data, binding.ContentSHA256Field))
	byteSize := int64Field(fileRecord.Data, binding.ByteSizeField)
	if fileID == "" || filename == "" || mediaType == "" || contentSHA256 == "" || byteSize < 1 || byteSize > maxPublicResourceFileBytes || service.files == nil || service.scans == nil {
		return publicresourcemodel.File{}, ErrNotFound
	}
	evidence, err := service.scans.Status(ctx, projection.WorkspaceID, fileID)
	if err != nil || evidence.Status != lifecyclecontract.FileScanClean || evidence.Size != byteSize || !strings.EqualFold(evidence.SHA256, contentSHA256) || !strings.EqualFold(evidence.ContentType, mediaType) {
		return publicresourcemodel.File{}, ErrNotFound
	}
	verified, err := service.files.OpenVerified(ctx, projection.WorkspaceID, runtimeext.VerifiedFileRequest{
		FileVerificationRequest: runtimeext.FileVerificationRequest{FileID: fileID, ContentSHA256: contentSHA256, ScanReceipt: evidence.Receipt},
		Binding:                 runtimeext.FileRecordBinding{ObjectKey: target.Key, RecordID: fileRecordID, FileIDField: binding.FileIDField},
	})
	if err != nil {
		return publicresourcemodel.File{}, ErrNotFound
	}
	defer verified.Content.Close()
	content, err := io.ReadAll(io.LimitReader(verified.Content, maxPublicResourceFileBytes+1))
	if err != nil {
		return publicresourcemodel.File{}, fmt.Errorf("read verified public resource file: %w", err)
	}
	if int64(len(content)) != byteSize || int64(len(content)) > maxPublicResourceFileBytes || !strings.EqualFold(verified.ContentSHA256, contentSHA256) || !strings.EqualFold(verified.ContentType, mediaType) {
		return publicresourcemodel.File{}, ErrNotFound
	}
	return publicresourcemodel.File{Filename: filename, ContentType: mediaType, Content: content}, nil
}

func (service *Service) resolve(resourceKey, accessKey string) (resourceDefinition, string, error) {
	resourceKey, accessKey = strings.TrimSpace(resourceKey), strings.TrimSpace(accessKey)
	if service == nil || service.repository == nil || service.records == nil {
		return resourceDefinition{}, "", fmt.Errorf("public resource service is unavailable")
	}
	if !publicResourceKeyRE.MatchString(resourceKey) || !publicAccessKeyRE.MatchString(accessKey) {
		return resourceDefinition{}, "", ErrRequestInvalid
	}
	definition, found := service.resources[resourceKey]
	if !found {
		return resourceDefinition{}, "", ErrNotFound
	}
	return definition, accessKey, nil
}

func objectField(object definitionmodel.ObjectSchema, key string) definitionmodel.FieldSchema {
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == strings.TrimSpace(key) {
			return field
		}
	}
	return definitionmodel.FieldSchema{}
}

func publicFileDisabled(data map[string]any, binding definitionmodel.ObjectPublicResourceFile) bool {
	if key := strings.TrimSpace(binding.DisabledBooleanField); key != "" {
		switch value := data[key].(type) {
		case bool:
			if value {
				return true
			}
		default:
			text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
			if text == "1" || text == "true" || text == "yes" || text == "on" {
				return true
			}
		}
	}
	if key := strings.TrimSpace(binding.DisabledTimestampField); key != "" && data[key] != nil && strings.TrimSpace(fmt.Sprint(data[key])) != "" {
		return true
	}
	return false
}

func stringField(data map[string]any, key string) string {
	value, exists := data[strings.TrimSpace(key)]
	if !exists || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func int64Field(data map[string]any, key string) int64 {
	value := data[strings.TrimSpace(key)]
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
		return parsed
	}
}
