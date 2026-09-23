package contract

// ApplicationSchemaFieldTypes returns the field kinds accepted by the project
// model and by code-registered operation payloads.
func ApplicationSchemaFieldTypes() []string {
	return []string{"boolean", "currency", "date", "datetime", "email", "file", "file_list", "integer", "json", "long_text", "multi_select", "number", "percent", "phone", "relation", "select", "text", "url", "user"}
}
