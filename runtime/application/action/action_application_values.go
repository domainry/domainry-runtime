package action

func actionMapValue(value any) map[string]any {
	typed, _ := value.(map[string]any)
	return typed
}

func actionFirstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
