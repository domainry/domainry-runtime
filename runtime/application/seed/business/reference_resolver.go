package businessseed

import (
	"context"
	"fmt"
	"strings"
)

// BaselineReferenceRequest describes one external reference required by a
// Runtime-generated business seed. The target comes from the manifest field
// contract; callers must not infer it from the field name.
type BaselineReferenceRequest struct {
	WorkspaceID       string
	ObjectKey         string
	FieldKey          string
	TargetObjectKey   string
	PreferredRecordID string
}

// BaselineReferenceResolver proves that a deterministic external record ID is
// valid for the current workspace before generated seed data can use it.
type BaselineReferenceResolver interface {
	ResolveBaselineReference(context.Context, BaselineReferenceRequest) (string, error)
}

type BaselineReferenceResolverFunc func(context.Context, BaselineReferenceRequest) (string, error)

func (resolve BaselineReferenceResolverFunc) ResolveBaselineReference(ctx context.Context, request BaselineReferenceRequest) (string, error) {
	return resolve(ctx, request)
}

// BaselineReferenceResolutionError is safe for startup diagnostics: it
// identifies the metadata boundary and aggregate candidate counts without
// exposing candidate payloads or acceptance credentials.
type BaselineReferenceResolutionError struct {
	WorkspaceID     string
	ObjectKey       string
	FieldKey        string
	TargetObjectKey string
	Reason          string
	CandidateCount  int
	ScopedCount     int
}

func (err *BaselineReferenceResolutionError) Error() string {
	if err == nil {
		return "baseline_reference_unresolved"
	}
	return fmt.Sprintf(
		"baseline_reference_unresolved object=%s field=%s target=%s workspace=%s reason=%s candidates=%d scoped_candidates=%d",
		safeBaselineReferenceDiagnostic(err.ObjectKey),
		safeBaselineReferenceDiagnostic(err.FieldKey),
		safeBaselineReferenceDiagnostic(err.TargetObjectKey),
		safeBaselineReferenceDiagnostic(err.WorkspaceID),
		safeBaselineReferenceDiagnostic(err.Reason),
		err.CandidateCount,
		err.ScopedCount,
	)
}

func safeBaselineReferenceDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unspecified"
	}
	var out strings.Builder
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9', character == '_', character == '-', character == '.':
			out.WriteRune(character)
		default:
			out.WriteByte('_')
		}
	}
	return out.String()
}
