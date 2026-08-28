package record

import (
	"errors"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestRecordLifecyclePolicies(t *testing.T) {
	softDelete := definitionmodel.ObjectSchema{
		Fields: []definitionmodel.FieldSchema{{Key: "status"}, {Key: "deleted_at"}, {Key: "deleted_by"}},
		Config: map[string]any{"restore_status": "enabled"},
	}
	if !recordpolicy.RecordUsesSoftDelete(softDelete) || recordpolicy.RecordRestoreStatus(softDelete) != "enabled" {
		t.Fatalf("soft delete lifecycle = uses %v restore %q", recordpolicy.RecordUsesSoftDelete(softDelete), recordpolicy.RecordRestoreStatus(softDelete))
	}
	if recordpolicy.RecordUsesSoftDelete(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "status"}}}) {
		t.Fatal("incomplete lifecycle fields enabled soft delete")
	}
	if recordpolicy.RecordRestoreStatus(definitionmodel.ObjectSchema{}) != "active" {
		t.Fatal("default restore status changed")
	}

	if !recordpolicy.RecordAutomationTransitionCandidate(map[string]any{"status": "draft"}, map[string]any{"status": "active"}) {
		t.Fatal("status transition was not detected")
	}
	if recordpolicy.RecordAutomationTransitionCandidate(map[string]any{"name": "before"}, map[string]any{"name": "after"}) {
		t.Fatal("ordinary update was classified as a transition")
	}
}

func TestValidateSchedulerOperationalCRUD(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "job_run", Config: map[string]any{"scheduler_runtime": true}}
	err := recordpolicy.RecordValidateSchedulerOperationalCRUD(object, "create")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindForbidden || appErr.Code != "backend.scheduler.runtime_api_required" {
		t.Fatalf("scheduler CRUD error = %#v", err)
	}
	if appErr.Params["object"] != "job_run" || appErr.Params["operation"] != "create" {
		t.Fatalf("scheduler CRUD params = %#v", appErr.Params)
	}
	if err := recordpolicy.RecordValidateSchedulerOperationalCRUD(definitionmodel.ObjectSchema{Key: "customer", Config: map[string]any{"scheduler_runtime": true}}, "update"); err != nil {
		t.Fatalf("unrelated object update rejected: %v", err)
	}
}

func TestRelationDeletePolicyDefaultsToRestrict(t *testing.T) {
	if got := recordpolicy.RecordRelationDeletePolicy(definitionmodel.FieldSchema{}); got != "restrict" {
		t.Fatalf("default policy = %q", got)
	}
	if got := recordpolicy.RecordRelationDeletePolicy(definitionmodel.FieldSchema{Config: map[string]any{"on_delete": "cascade"}}); got != "cascade" {
		t.Fatalf("configured policy = %q", got)
	}
}

func TestApplyAutoCodeDefaults(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "order_no", Config: map[string]any{"auto_code": map[string]any{"prefix": "SO", "time": "2006", "sequence": "0007"}}},
		{Key: "external_no", Config: map[string]any{"auto_code": "EXT"}},
		{Key: "manual_no", Config: map[string]any{"auto_code": false}},
	}}
	data := map[string]any{"order_no": ""}
	recordpolicy.RecordApplyAutoCodeDefaults(object, data, "customer_123456789")
	if code := data["order_no"].(string); !strings.HasPrefix(code, "SO-") || !strings.HasSuffix(code, "-0007") || len(strings.Split(code, "-")) != 3 {
		t.Fatalf("configured auto code = %q", code)
	}
	if code := data["external_no"].(string); !strings.HasPrefix(code, "EXT-") || !strings.HasSuffix(code, "23456789") {
		t.Fatalf("record-id auto code = %q", code)
	}
	if _, exists := data["manual_no"]; exists {
		t.Fatalf("disabled auto code populated: %#v", data)
	}

	data = map[string]any{"order_no": "MANUAL"}
	recordpolicy.RecordApplyAutoCodeDefaults(object, data, "customer_1")
	if data["order_no"] != "MANUAL" {
		t.Fatalf("manual value overwritten: %#v", data)
	}
}
