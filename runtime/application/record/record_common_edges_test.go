package record

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type recordCodedError struct {
	code   string
	params map[string]string
}

func (e recordCodedError) Error() string                  { return "coded record failure" }
func (e recordCodedError) ErrorCode() string              { return e.code }
func (e recordCodedError) ErrorParams() map[string]string { return e.params }

func TestRecordCreateAndDeleteErrorConversion(t *testing.T) {
	existing := &apperror.AppError{Kind: apperror.KindConflict, Code: "record.conflict"}
	coded := recordCodedError{code: " record.invalid ", params: map[string]string{"field": "name"}}
	plain := errors.New("plain failure")

	for _, test := range []struct {
		name     string
		convert  func(apperror.ErrorKind, error) error
		kind     apperror.ErrorKind
		input    error
		wantCode string
		wantSame bool
	}{
		{name: "create nil", convert: recordCreateErrorFrom, kind: apperror.KindBadRequest, input: nil},
		{name: "create preserves app error", convert: recordCreateErrorFrom, kind: apperror.KindBadRequest, input: existing, wantCode: "record.conflict", wantSame: true},
		{name: "create preserves coded error", convert: recordCreateErrorFrom, kind: apperror.KindBadRequest, input: coded, wantCode: "record.invalid"},
		{name: "create generic forbidden", convert: recordCreateErrorFrom, kind: apperror.KindForbidden, input: plain, wantCode: "backend.forbidden"},
		{name: "create generic invalid", convert: recordCreateErrorFrom, kind: apperror.KindBadRequest, input: plain, wantCode: "backend.bad_request"},
		{name: "delete nil", convert: recordDeleteErrorFrom, kind: apperror.KindBadRequest, input: nil},
		{name: "delete preserves app error", convert: recordDeleteErrorFrom, kind: apperror.KindBadRequest, input: existing, wantCode: "record.conflict", wantSame: true},
		{name: "delete preserves coded error", convert: recordDeleteErrorFrom, kind: apperror.KindBadRequest, input: coded, wantCode: "record.invalid"},
		{name: "delete generic forbidden", convert: recordDeleteErrorFrom, kind: apperror.KindForbidden, input: plain, wantCode: "backend.forbidden"},
		{name: "delete generic invalid", convert: recordDeleteErrorFrom, kind: apperror.KindBadRequest, input: plain, wantCode: "backend.bad_request"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := test.convert(test.kind, test.input)
			if test.input == nil {
				if got != nil {
					t.Fatalf("got=%v, want nil", got)
				}
				return
			}
			if test.wantSame && got != test.input {
				t.Fatalf("error identity changed: got=%p want=%p", got, test.input)
			}
			if apperror.CodeOf(got) != test.wantCode || apperror.KindOf(got) != test.kind && !test.wantSame {
				t.Fatalf("got=%#v kind=%q code=%q", got, apperror.KindOf(got), apperror.CodeOf(got))
			}
			if !test.wantSame && errors.Unwrap(got) == nil {
				t.Fatalf("converted error does not wrap input: %v", got)
			}
		})
	}

	createInternal := recordCreateError(apperror.KindInternal, "backend.internal", plain, "", "ignored", "operation", "create", "dangling")
	deleteInternal := recordDeleteInternalError("delete", plain)
	if apperror.ParamsOf(createInternal)["operation"] != "create" || apperror.ParamsOf(deleteInternal)["operation"] != "delete" {
		t.Fatalf("create=%#v delete=%#v", createInternal, deleteInternal)
	}
	if params := apperror.ParamsOf(recordDeleteError(apperror.KindBadRequest, "backend.bad_request", plain)); params != nil {
		t.Fatalf("empty params=%#v", params)
	}
}

func TestRecordDeleteIntAcceptedRepresentations(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  int
		ok    bool
	}{
		{name: "int", value: 3, want: 3, ok: true},
		{name: "int64", value: int64(4), want: 4, ok: true},
		{name: "float64", value: float64(5), want: 5, ok: true},
		{name: "json number", value: json.Number("6"), want: 6, ok: true},
		{name: "trimmed string", value: " 7 ", want: 7, ok: true},
		{name: "invalid json number", value: json.Number("6.5")},
		{name: "invalid string", value: "six"},
		{name: "unsupported", value: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := recordDeleteInt(test.value)
			if got != test.want || ok != test.ok {
				t.Fatalf("got=(%d,%v), want=(%d,%v)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestRestrictExportFieldsNormalizesRequestedKeys(t *testing.T) {
	fields := []definitionmodel.FieldSchema{{Key: "id"}, {Key: "name"}, {Key: "status"}}
	if got := restrictExportFields(fields, []string{" ", "name", " name "}); len(got) != 1 || got[0].Key != "name" {
		t.Fatalf("restricted fields=%#v", got)
	}
	if got := restrictExportFields(fields, nil); len(got) != len(fields) {
		t.Fatalf("default fields=%#v", got)
	}
}

func TestRecordImportErrorAndBatchYieldEdges(t *testing.T) {
	if issue := importRowIssueFromError("name", nil); issue.Code != "backend.bad_request" || issue.Field != "name" {
		t.Fatalf("nil issue=%#v", issue)
	}
	if issue := importRowIssueFromError("name", recordCodedError{code: " record.invalid ", params: map[string]string{"field": "name"}}); issue.Code != "record.invalid" || issue.Params["field"] != "name" {
		t.Fatalf("coded issue=%#v", issue)
	}
	if issue := importRowIssueFromError("name", errors.New("plain")); issue.Code != "backend.bad_request" {
		t.Fatalf("plain issue=%#v", issue)
	}
	if issue := importRowIssue("name", "warning", "", "", "ignored", "row", "2", "dangling"); issue.Code != "backend.bad_request" || issue.Params["row"] != "2" {
		t.Fatalf("issue=%#v", issue)
	}
	if err := recordImportBatchYield(t.Context(), 1); err != nil {
		t.Fatalf("non-boundary yield=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := recordImportBatchYield(cancelled, recordImportBatchSize); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled yield=%v", err)
	}
}

func TestRecordCreateDefaultIDAndClock(t *testing.T) {
	service := NewRecordCreateApplicationService(RecordCreateDependencies{})
	if id := service.newRecordID("customer"); !strings.HasPrefix(id, "customer_") {
		t.Fatalf("id=%q", id)
	}
	before := time.Now().Add(-time.Second)
	got := service.now()
	if got.Before(before) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("now=%v", got)
	}
}

func TestRecordUpdateAndRestoreErrorConversion(t *testing.T) {
	existing := &apperror.AppError{Kind: apperror.KindConflict, Code: "record.conflict"}
	coded := recordCodedError{code: " record.invalid ", params: map[string]string{"field": "name"}}
	plain := errors.New("plain failure")
	for _, convert := range []struct {
		name string
		call func(apperror.ErrorKind, error) error
	}{
		{name: "update", call: recordUpdateErrorFrom},
		{name: "restore", call: recordRestoreErrorFrom},
	} {
		t.Run(convert.name, func(t *testing.T) {
			if err := convert.call(apperror.KindBadRequest, nil); err != nil {
				t.Fatalf("nil err=%v", err)
			}
			if err := convert.call(apperror.KindBadRequest, existing); err != existing {
				t.Fatalf("existing error identity changed: %v", err)
			}
			if err := convert.call(apperror.KindBadRequest, coded); apperror.CodeOf(err) != "record.invalid" || apperror.ParamsOf(err)["field"] != "name" {
				t.Fatalf("coded err=%#v", err)
			}
			if err := convert.call(apperror.KindForbidden, plain); apperror.CodeOf(err) != "backend.forbidden" || !errors.Is(err, plain) {
				t.Fatalf("forbidden err=%#v", err)
			}
			if err := convert.call(apperror.KindBadRequest, plain); apperror.CodeOf(err) != "backend.bad_request" || !errors.Is(err, plain) {
				t.Fatalf("bad request err=%#v", err)
			}
		})
	}
}

func TestRecordUpdateAndRestoreIntAcceptedRepresentations(t *testing.T) {
	parsers := []struct {
		name string
		call func(any) (int, bool)
	}{
		{name: "update", call: recordUpdateInt},
		{name: "restore", call: restoreInt},
	}
	values := []struct {
		name  string
		value any
		want  int
		ok    bool
	}{
		{name: "int", value: 3, want: 3, ok: true},
		{name: "int64", value: int64(4), want: 4, ok: true},
		{name: "float64", value: float64(5), want: 5, ok: true},
		{name: "json number", value: json.Number("6"), want: 6, ok: true},
		{name: "string", value: " 7 ", want: 7, ok: true},
		{name: "invalid number", value: json.Number("6.5")},
		{name: "invalid string", value: "six"},
		{name: "unsupported", value: true},
	}
	for _, parser := range parsers {
		for _, value := range values {
			t.Run(parser.name+"/"+value.name, func(t *testing.T) {
				got, ok := parser.call(value.value)
				if got != value.want || ok != value.ok {
					t.Fatalf("got=(%d,%v), want=(%d,%v)", got, ok, value.want, value.ok)
				}
			})
		}
	}
}
