package service

import (
	"errors"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestMetadataErrorConstructorsPreserveKindsAndSanitizeParams(t *testing.T) {
	tests := []struct {
		name string
		make func(string, ...string) error
		kind apperror.ErrorKind
	}{
		{name: "bad request", make: badRequest, kind: apperror.KindBadRequest},
		{name: "forbidden", make: forbidden, kind: apperror.KindForbidden},
		{name: "not found", make: notFound, kind: apperror.KindNotFound},
		{name: "conflict", make: conflict, kind: apperror.KindConflict},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.make(
				"backend.metadata.test",
				"field", "client_secret",
				"value", "must-not-leak",
				"safe", "visible",
				" ", "ignored",
				"unpaired",
			)

			if got := apperror.KindOf(err); got != test.kind {
				t.Fatalf("kind = %q, want %q", got, test.kind)
			}
			if got := apperror.CodeOf(err); got != "backend.metadata.test" {
				t.Fatalf("code = %q", got)
			}
			want := map[string]string{
				"field": "client_secret",
				"value": "[REDACTED]",
				"safe":  "visible",
			}
			if got := apperror.ParamsOf(err); !reflect.DeepEqual(got, want) {
				t.Fatalf("params = %#v, want %#v", got, want)
			}
		})
	}
}

func TestMetadataErrorOmitsEmptyParams(t *testing.T) {
	err := metadataError(apperror.KindBadRequest, "backend.metadata.test", " ", "ignored", "unpaired")
	if params := apperror.ParamsOf(err); params != nil {
		t.Fatalf("params = %#v, want nil", params)
	}
}

func TestMetadataInternalError(t *testing.T) {
	err := metadataInternalError("load schema")
	if got := apperror.KindOf(err); got != apperror.KindInternal {
		t.Fatalf("kind = %q", got)
	}
	if got := apperror.CodeOf(err); got != "backend.internal" {
		t.Fatalf("code = %q", got)
	}
	if got := apperror.ParamsOf(err)["operation"]; got != "load schema" {
		t.Fatalf("operation = %q", got)
	}
}

func TestWrapMetadataError(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if err := wrapMetadataError(nil); err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	})

	t.Run("application error", func(t *testing.T) {
		source := &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.metadata.missing"}
		if got := wrapMetadataError(source); got != source {
			t.Fatalf("wrapped application error = %v, want original", got)
		}
	})

	t.Run("repository error", func(t *testing.T) {
		source := errors.New("database unavailable")
		err := wrapMetadataError(source)
		if !errors.Is(err, source) {
			t.Fatalf("wrapped error %v does not contain source", err)
		}
		if got := apperror.KindOf(err); got != apperror.KindInternal {
			t.Fatalf("kind = %q", got)
		}
		if got := apperror.CodeOf(err); got != "backend.internal" {
			t.Fatalf("code = %q", got)
		}
		if got := apperror.ParamsOf(err)["operation"]; got != "metadata repository operation" {
			t.Fatalf("operation = %q", got)
		}
	})
}

func TestMetadataValueOrDefault(t *testing.T) {
	if got := valueOrDefault("  configured  ", "fallback"); got != "configured" {
		t.Fatalf("configured value = %q", got)
	}
	if got := valueOrDefault("  ", "fallback"); got != "fallback" {
		t.Fatalf("fallback value = %q", got)
	}
}
