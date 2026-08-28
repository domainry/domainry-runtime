package policy

import (
	"regexp"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRecordApplyAutoCodeDefaultsAndRuleShapes(t *testing.T) {
	RecordApplyAutoCodeDefaults(definitionmodel.ObjectSchema{}, nil, "record")
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: ""},
		{Key: "existing", Config: map[string]any{"auto_code": true}},
		{Key: "none"},
		{Key: "map_any", Config: map[string]any{"auto_code": map[string]any{"prefix": " ORD ", "time": "", "time_format": "2006", "sequence": " 7 "}}},
		{Key: "map_string", Config: map[string]any{"autoCode": map[string]string{"prefix": "INV", "timeFormat": "01", "seq": "8"}}},
		{Key: "enabled", Config: map[string]any{"auto_code": true, "prefix": "GEN"}},
		{Key: "disabled", Config: map[string]any{"auto_code": false}},
		{Key: "string", Config: map[string]any{"auto_code": "CUS"}},
	}}
	data := map[string]any{"existing": "keep"}
	RecordApplyAutoCodeDefaults(object, data, "record-123456789")
	if data["existing"] != "keep" || data["none"] != nil || data["disabled"] != nil {
		t.Fatalf("preserved values = %#v", data)
	}
	if !strings.HasPrefix(data["map_any"].(string), "ORD-") || !strings.HasSuffix(data["map_any"].(string), "-7") || !strings.HasPrefix(data["map_string"].(string), "INV-") || !strings.HasSuffix(data["map_string"].(string), "-8") || !strings.HasPrefix(data["enabled"].(string), "GEN-") || !strings.HasPrefix(data["string"].(string), "CUS-") {
		t.Fatalf("generated values = %#v", data)
	}
}

func TestRecordAutoCodeRuleAndValueHelpersMatrix(t *testing.T) {
	for _, test := range []struct {
		field definitionmodel.FieldSchema
		ok    bool
	}{
		{field: definitionmodel.FieldSchema{}, ok: false},
		{field: definitionmodel.FieldSchema{Config: map[string]any{}}, ok: false},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"auto_code": nil}}, ok: false},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"auto_code": ""}}, ok: false},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"auto_code": "false"}}, ok: false},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"auto_code": 1}}, ok: true},
	} {
		if _, ok := autoCodeRuleForField(test.field); ok != test.ok {
			t.Fatalf("rule %#v = %v", test.field, ok)
		}
	}
	if got := generateAutoCodeValue(autoCodeRule{TimePart: "", Sequence: "fixed"}, "record"); got != "fixed" {
		t.Fatalf("sequence-only code = %q", got)
	}
	if got := generateAutoCodeValue(autoCodeRule{Prefix: "P", TimePart: "2006"}, "record-123456789"); !regexp.MustCompile(`^P-[0-9]{4}-23456789$`).MatchString(got) {
		t.Fatalf("generated compact code = %q", got)
	}
	if compactAutoCodeSequence(" short ") != "short" || compactAutoCodeSequence("123456789") != "23456789" || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(compactAutoCodeSequence("")) {
		t.Fatal("compact sequence mismatch")
	}
	if got := firstNonEmptyConfigString(map[string]any{"one": nil, "two": " value "}, "one", "two"); got != "value" || firstNonEmptyConfigString(nil, "one") != "" {
		t.Fatal("config string fallback mismatch")
	}
	if firstNonEmptyString(" ", " value ") != "value" || firstNonEmptyString(" ") != "" {
		t.Fatal("string fallback mismatch")
	}
	for _, test := range []struct {
		value any
		want  bool
	}{{nil, true}, {"", true}, {" ", true}, {"value", false}, {0, false}} {
		if got := recordAutoCodeValueEmpty(test.value); got != test.want {
			t.Fatalf("empty %#v = %v", test.value, got)
		}
	}
}
