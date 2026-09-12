package businessseedmodel

import (
	"encoding/json"
	"testing"
)

func TestSeedRecordJSONPreservesLargeAndFractionalNumbers(t *testing.T) {
	var seed SeedRecordSchema
	raw := []byte(`{"object_key":"sale","data":{"amount":9007199254740993,"fraction":0.123456789012345678,"nested":[9007199254740995],"empty":null}}`)
	if err := json.Unmarshal(raw, &seed); err != nil {
		t.Fatal(err)
	}
	if seed.Data["amount"] != json.Number("9007199254740993") || seed.Data["fraction"] != json.Number("0.123456789012345678") || seed.Data["nested"].([]any)[0] != json.Number("9007199254740995") || seed.Data["empty"] != nil {
		t.Fatal(seed.Data)
	}
	for _, raw := range []string{`{"object_key":"sale","data":{},"unknown":true}`, `{"object_key":"sale","data":{}} {}`} {
		if err := json.Unmarshal([]byte(raw), &seed); err == nil {
			t.Fatal("invalid seed accepted", raw)
		}
	}
}
