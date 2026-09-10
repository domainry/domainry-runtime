package validation

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRecordPlainTextNormalizationPreservesBusinessValues(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "vendor", Fields: []definitionmodel.FieldSchema{{Key: "code", Type: "text"}, {Key: "note", Type: "long_text"}}}
	input := map[string]any{"code": "  original code  ", "note": "\n  note\n"}
	for _, partial := range []bool{false, true} {
		got, err := RecordNormalizeData(object, input, partial)
		if err != nil || !reflect.DeepEqual(got, input) {
			t.Errorf("partial=%v got=%#v err=%v want=%#v", partial, got, err, input)
		}
	}
}
