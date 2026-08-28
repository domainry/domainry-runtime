package record_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

func TestGymPRDSection2FiveRoleRBACMatrixUsesGenericRuntimePermissions(t *testing.T) {
	const manager, receptionist, coach, sales, member = "manager", "receptionist", "coach", "sales", "member"
	roles := []string{manager, receptionist, coach, sales, member}
	matrix := []struct {
		object string
		grants map[string]string
	}{
		{"gym_member", map[string]string{manager: "crud", receptionist: "cru", coach: "r", sales: "cru", member: "r"}},
		{"gym_student", map[string]string{manager: "crud", receptionist: "cru", coach: "r", sales: "cru", member: "r"}},
		{"membership_card_type", map[string]string{manager: "crud", receptionist: "r", sales: "r"}},
		{"membership_card", map[string]string{manager: "cruda", receptionist: "cru", coach: "r", sales: "cr", member: "r"}},
		{"stored_value_account", map[string]string{manager: "crud", receptionist: "cru", sales: "r", member: "r"}},
		{"stored_value_transaction", map[string]string{manager: "crud", receptionist: "cr", member: "r"}},
		{"personal_training_package", map[string]string{manager: "crud", receptionist: "cr", coach: "r", sales: "cr", member: "r"}},
		{"personal_training_session", map[string]string{manager: "ra", receptionist: "c", coach: "ru", member: "r"}},
		{"group_class", map[string]string{manager: "crud", receptionist: "cru", coach: "r", member: "r"}},
		{"group_class_booking", map[string]string{manager: "crud", receptionist: "cru", coach: "r", member: "cr"}},
		{"equipment", map[string]string{manager: "crud", receptionist: "ru", coach: "r"}},
		{"equipment_maintenance", map[string]string{manager: "crud", receptionist: "r", coach: "r"}},
		{"equipment_repair_ticket", map[string]string{manager: "crud", receptionist: "cr", coach: "r"}},
		{"access_record", map[string]string{manager: "crud", receptionist: "cr", coach: "r", member: "r"}},
		{"class_check_in", map[string]string{manager: "crud", receptionist: "cru", coach: "ru"}},
		{"sales_order", map[string]string{manager: "cruda", receptionist: "cru", coach: "r", sales: "cr", member: "r"}},
		{"payment", map[string]string{manager: "crud", receptionist: "cru", member: "r"}},
		{"refund", map[string]string{manager: "cruda", receptionist: "c", member: "r"}},
		{"coach_commission_settlement", map[string]string{manager: "cruda", receptionist: "r", coach: "r"}},
		{"coupon_experience_voucher_referral", map[string]string{manager: "crud", receptionist: "cru", coach: "r", sales: "cr", member: "r"}},
	}
	objects := make([]definitionmodel.ObjectSchema, 0, len(matrix))
	roleSchemas := map[string]accessfixture.Bundle{}
	for _, role := range roles {
		roleSchemas[role] = accessfixture.Bundle{Key: role}
	}
	for _, row := range matrix {
		objects = append(objects, definitionmodel.ObjectSchema{Key: row.object})
		for _, role := range roles {
			grant := row.grants[role]
			schema := roleSchemas[role]
			dataPermission := accessfixture.DataPolicyFixture{ObjectKey: row.object, Scope: "all_records", Read: strings.Contains(grant, "r"), Write: strings.ContainsAny(grant, "cuda")}
			if grant != "" {
				schema.DataPolicies = append(schema.DataPolicies, dataPermission)
			}
			for code, action := range map[string]string{"c": "create", "r": "read", "u": "update", "d": "delete", "a": "approve"} {
				if strings.Contains(grant, code) {
					schema.Permissions = append(schema.Permissions, row.object+"."+action)
				}
			}
			roleSchemas[role] = schema
		}
	}
	policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return objects }})
	for _, row := range matrix {
		for _, role := range roles {
			for code, action := range map[string]string{"c": "create", "r": "read", "u": "update", "d": "delete", "a": "approve"} {
				_, err := policy.ObjectForAction(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, roleSchemas[role]), row.object, action)
				allowed := strings.Contains(row.grants[role], code)
				if (err == nil) != allowed {
					t.Fatalf("PRD RBAC mismatch object=%s role=%s action=%s grant=%q err=%v", row.object, role, action, row.grants[role], err)
				}
			}
		}
	}
}

func TestGymPRDSection2FiveRoleCLSMatrixUsesGenericContextualFieldPolicies(t *testing.T) {
	const clear, hide, phone, idNumber, yearOnly, genericMask = "clear", "hide", "phone", "id_number", "year_only", "generic_mask"
	roles := []string{"manager", "receptionist", "coach", "sales", "member"}
	type matrixRow struct {
		object, field, fieldType string
		value                    any
		expect                   map[string]string
	}
	rows := []matrixRow{
		{"gym_member", "mobile", "phone", "10000000004", map[string]string{"manager": clear, "receptionist": clear, "coach": phone, "sales": clear, "member": clear}},
		{"gym_member", "id_number", "text", "000000000000000000", map[string]string{"manager": clear, "receptionist": idNumber, "coach": hide, "sales": idNumber, "member": clear}},
		{"gym_member", "address", "text", "Shanghai", map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": clear, "member": clear}},
		{"gym_member", "birth_date", "date", "1990-01-02", map[string]string{"manager": clear, "receptionist": clear, "coach": yearOnly, "sales": clear, "member": clear}},
		{"gym_member", "health_profile", "text", "knee injury", map[string]string{"manager": clear, "receptionist": genericMask, "coach": clear, "sales": hide, "member": clear}},
		{"gym_member", "emergency_contacts", "text", "Alice 10000000001", map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": clear, "member": clear}},
		{"gym_student", "mobile", "phone", "10000000006", map[string]string{"manager": clear, "receptionist": clear, "coach": phone, "sales": clear, "member": clear}},
		{"gym_student", "body_tests", "text", "body-fat 18", map[string]string{"manager": clear, "receptionist": hide, "coach": clear, "sales": hide, "member": clear}},
		{"stored_value_account", "balance", "currency", "100.00", map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": hide, "member": clear}},
		{"stored_value_account", "bonus_balance", "currency", "20.00", map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": hide, "member": clear}},
		{"payment", "amount", "currency", "88.00", map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": hide, "member": clear}},
		{"payment", "txn_ref", "text", "TXN-001", map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": hide, "member": clear}},
		{"sales_order", "payable_paid", "currency", "188.00", map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": clear, "member": clear}},
		{"refund", "amount", "currency", "20.00", map[string]string{"manager": clear, "receptionist": hide, "coach": hide, "sales": hide, "member": clear}},
		{"coach_commission_settlement", "total_amount", "currency", "500.00", map[string]string{"manager": clear, "receptionist": hide, "coach": clear, "sales": hide, "member": hide}},
		{"membership_card", "remaining_entries", "integer", 12, map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": clear, "member": clear}},
		{"gym_member", "is_blacklist", "boolean", true, map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": hide, "member": hide}},
		{"gym_member", "tags", "text", "VIP", map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": clear, "member": hide}},
		{"access_record", "reject_reason", "text", "expired", map[string]string{"manager": clear, "receptionist": clear, "coach": hide, "sales": hide, "member": clear}},
		{"referral", "referee_mobile", "phone", "10000000007", map[string]string{"manager": clear, "receptionist": phone, "coach": hide, "sales": clear, "member": clear}},
	}
	objectsByKey := map[string]definitionmodel.ObjectSchema{}
	roleSchemas := map[string]accessfixture.Bundle{}
	valuesByObject := map[string]map[string]any{}
	for _, row := range rows {
		object := objectsByKey[row.object]
		object.Key = row.object
		object.Fields = append(object.Fields, definitionmodel.FieldSchema{Key: row.field, Type: row.fieldType})
		objectsByKey[row.object] = object
		if valuesByObject[row.object] == nil {
			valuesByObject[row.object] = map[string]any{}
		}
		valuesByObject[row.object][row.field] = row.value
		for _, role := range roles {
			permission := accessfixture.FieldPolicyFixture{ObjectKey: row.object, FieldKey: row.field}
			expected := row.expect[role]
			if expected != hide {
				permission.Read = true
			}
			if expected != clear && expected != hide {
				strategy := accessfixture.MaskFixture{Type: expected}
				if expected == genericMask {
					strategy = accessfixture.MaskFixture{Type: "last_n", LastN: 2}
				}
				permission.Policies = []accessfixture.FieldRuleFixture{{Key: row.object + "-" + row.field + "-mask", Priority: 10, Actions: []string{"read", "export", "report", "audit"}, Effect: "mask", MaskStrategy: &strategy}}
			}
			schema := roleSchemas[role]
			schema.Key = role
			schema.FieldPolicies = append(schema.FieldPolicies, permission)
			roleSchemas[role] = schema
		}
	}
	objects := make([]definitionmodel.ObjectSchema, 0, len(objectsByKey))
	for _, object := range objectsByKey {
		objects = append(objects, object)
	}
	fieldPolicy := recordservice.NewRecordContextualFieldPolicyDomainService(recordservice.RecordContextualFieldPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return objects }})
	for _, role := range roles {
		principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, roleSchemas[role])
		for objectKey, data := range valuesByObject {
			projected, _, err := fieldPolicy.ApplyRead(context.Background(), principal, objectsByKey[objectKey], recordmodel.Record{ID: "authorized-row", Data: data}, "read")
			if err != nil {
				t.Fatalf("CLS role=%s object=%s: %v", role, objectKey, err)
			}
			for _, row := range rows {
				if row.object != objectKey {
					continue
				}
				got, present := projected.Data[row.field]
				expected := row.expect[role]
				switch expected {
				case clear:
					if !present || fmt.Sprint(got) != fmt.Sprint(row.value) {
						t.Fatalf("CLS clear mismatch role=%s field=%s.%s got=%#v", role, objectKey, row.field, got)
					}
				case hide:
					if present {
						t.Fatalf("CLS hidden field leaked role=%s field=%s.%s got=%#v", role, objectKey, row.field, got)
					}
				default:
					if !present || fmt.Sprint(got) == fmt.Sprint(row.value) || strings.TrimSpace(fmt.Sprint(got)) == "" {
						t.Fatalf("CLS mask mismatch role=%s field=%s.%s raw=%#v got=%#v", role, objectKey, row.field, row.value, got)
					}
				}
			}
		}
	}
}
