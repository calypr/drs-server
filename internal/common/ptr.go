package common

import "time"

// Ptr returns a pointer to the value passed in.
func Ptr[T any](v T) *T {
	return &v
}



// TimeVal returns the time value if not nil, otherwise zero time.
func TimeVal(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

// StringVal returns the string value if not nil, otherwise empty string.
func StringVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// DerefString is a legacy alias for StringVal.
func DerefString(p *string) string {
	return StringVal(p)
}

// DerefStringSlice returns a copy of the slice or nil when the pointer is nil.
func DerefStringSlice(p *[]string) []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), (*p)...)
}



// Int64Val returns the int64 value if not nil, otherwise 0.
func Int64Val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}


