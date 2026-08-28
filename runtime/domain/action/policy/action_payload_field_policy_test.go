package policy

import (
	"reflect"
	"testing"
)

func TestActionMapPayloadFieldCompleteMatrix(t *testing.T) {
	if got := ActionMapPayloadField(nil); got.Key != "" {
		t.Fatalf("nil payload field = %#v", got)
	}
	if got := ActionMapPayloadField(map[string]any{"key": "field"}); got.Key != "field" || got.Name != "field" || got.Type != "text" || got.Required || got.Default != nil || got.DefaultValue != nil || len(got.Config) != 0 {
		t.Fatalf("default payload field = %#v", got)
	}
	raw := map[string]any{
		"key":           " status ",
		"name":          " Status ",
		"type":          " select ",
		"required":      true,
		"default":       "open",
		"default_value": "fallback",
		"options":       []string{"open", "closed"},
	}
	got := ActionMapPayloadField(raw)
	if got.Key != "status" || got.Name != "Status" || got.Type != "select" || !got.Required || got.Default != "open" || got.DefaultValue != "fallback" || !reflect.DeepEqual(got.Config, map[string]any{"options": []string{"open", "closed"}}) {
		t.Fatalf("mapped payload field = %#v", got)
	}
	raw["options"] = []string{"mutated"}
	if !reflect.DeepEqual(got.Config["options"], []string{"open", "closed"}) {
		t.Fatalf("mapped config changed with source: %#v", got.Config)
	}
	got = ActionMapPayloadField(map[string]any{"key": "count", "default_value": 3, "required": "yes"})
	if got.Default != 3 || got.Required {
		t.Fatalf("default_value fallback = %#v", got)
	}
}

func TestActionValueNormalizationCompleteMatrix(t *testing.T) {
	if ActionNormalizedValue(nil) != "" || ActionNormalizedValue((*int)(nil)) != "" || ActionNormalizedValue(" value ") != "value" || ActionNormalizedValue(12) != "12" {
		t.Fatal("normalized value matrix mismatch")
	}
	source := []string{" a ", "b"}
	got := ActionStringList(source)
	if !reflect.DeepEqual(got, source) {
		t.Fatalf("string list = %#v", got)
	}
	got[0] = "changed"
	if source[0] != " a " {
		t.Fatal("string list result aliases source")
	}
	if got := ActionStringList([]any{" a ", nil, "", "<nil>", 2}); !reflect.DeepEqual(got, []string{"a", "2"}) {
		t.Fatalf("any list = %#v", got)
	}
	if got := ActionStringList("a,b"); got != nil {
		t.Fatalf("unsupported list = %#v", got)
	}
}
