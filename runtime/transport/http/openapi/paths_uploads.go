package openapi

func addUploadOpenAPIPaths(paths map[string]any) {
	paths["/files"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"uploadFile", "Files", "Upload an attachment file under the authorized object/field scope; the initial scan_status is always pending", openAPIAdminSecurity(),
		openAPIRequiredQueryParameter("object_key", "Object owning the file field", map[string]any{"type": "string"}), openAPIRequiredQueryParameter("field_key", "Authorized file field", map[string]any{"type": "string"}),
		openAPIRequestBody{Value: map[string]any{"required": true, "content": map[string]any{"multipart/form-data": map[string]any{"schema": openAPIRequiredObject([]string{"file"}, map[string]any{"file": map[string]any{"type": "string", "format": "binary"}})}}}},
		openAPIJSONResponse("Uploaded file identity", openAPIRequiredObject([]string{"file_id", "content_sha256", "scan_status", "url", "filename", "content_type", "size"}, map[string]any{
			"file_id": map[string]any{"type": "string"}, "content_sha256": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"}, "scan_status": map[string]any{"type": "string"}, "url": map[string]any{"type": "string"}, "filename": map[string]any{"type": "string"}, "content_type": map[string]any{"type": "string"}, "size": map[string]any{"type": "integer", "format": "int64"}, "object_key": map[string]any{"type": "string"}, "field_key": map[string]any{"type": "string"},
		})),
	), "uploadFile")}
	paths["/files/{fileID}/scan"] = map[string]any{"get": openAPIRuntimeClient(openAPIOperation("getFileScan", "Files", "Read Runtime-owned scan status and a signed receipt only when clean", openAPIAdminSecurity(), openAPIPathParameter("fileID", "Runtime file identity"), openAPIJSONResponse("File scan evidence", openAPIObject(nil))), "getFileScan")}
	paths["/uploads/{filename}"] = map[string]any{
		"get": openAPIRuntimeClient(openAPIOperation("downloadUpload", "Files", "Download an uploaded attachment file under the same authorized object/field scope", openAPIAdminSecurity(), openAPIPathParameter("filename", "Uploaded filename"), openAPIRequiredQueryParameter("object_key", "Object owning the file field", map[string]any{"type": "string"}), openAPIRequiredQueryParameter("field_key", "Authorized file field", map[string]any{"type": "string"}), openAPIQueryParameter("record_id", "Optional record that must reference this file", map[string]any{"type": "string"}), openAPIResponse("File", "application/octet-stream", map[string]any{"type": "string", "format": "binary"})), "downloadUpload"),
	}
}
