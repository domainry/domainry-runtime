package contract

func ApplicationSchemaAuthoringFieldTypes() []string {
	return []string{"boolean", "currency", "date", "datetime", "email", "integer", "long_text", "number", "percent", "phone", "relation", "select", "text", "url", "user"}
}

func metadataFloatPointer(value float64) *float64 { return &value }
func metadataIntPointer(value int) *int           { return &value }
