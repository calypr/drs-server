package drs

func drsPtr[T any](value T) *T {
	return &value
}

func drsStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
