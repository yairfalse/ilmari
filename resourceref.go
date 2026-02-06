package ilmari

import "strings"

// ResourceRef is a value object for "kind/name" resource references.
// It provides type-safe parsing and eliminates the primitive obsession
// of passing raw strings through the codebase.
type ResourceRef struct {
	Kind string // lowercase: "deployment", "pod", "service", etc.
	Name string
}

// ParseResourceRef parses a "kind/name" string into a ResourceRef.
// Returns a ResourceFormatError if the format is invalid.
func ParseResourceRef(s string) (ResourceRef, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ResourceRef{}, newResourceFormatError(s)
	}
	return ResourceRef{Kind: strings.ToLower(parts[0]), Name: parts[1]}, nil
}

// MustParseResourceRef parses a "kind/name" string or panics.
// Use in tests and builders where the input is known to be valid.
func MustParseResourceRef(s string) ResourceRef {
	ref, err := ParseResourceRef(s)
	if err != nil {
		panic(err)
	}
	return ref
}

// ParseResourceRefWithDefault handles "name" or "kind/name" strings.
// If no "/" is present, uses defaultKind as the kind.
// Used by Kill() (default "pod") and WaitPVCBound() (default "pvc").
func ParseResourceRefWithDefault(s string, defaultKind string) ResourceRef {
	if !strings.Contains(s, "/") {
		return ResourceRef{Kind: strings.ToLower(defaultKind), Name: s}
	}
	ref, err := ParseResourceRef(s)
	if err != nil {
		// Fallback: if parsing fails, treat whole string as name
		return ResourceRef{Kind: strings.ToLower(defaultKind), Name: s}
	}
	return ref
}

// String returns the "kind/name" representation.
func (r ResourceRef) String() string {
	return r.Kind + "/" + r.Name
}
