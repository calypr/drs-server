package objects

func objectStringPtr(value string) *string { return &value }

func objectStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func objectInt64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func objectStringSliceValue(value *[]string) []string {
	if value == nil {
		return nil
	}
	return *value
}
