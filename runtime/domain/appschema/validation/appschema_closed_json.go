package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// decodeClosedAuthoringJSON enforces the same closed-object contract published
// by Runtime capability schemas. Silent unknown-field acceptance would make a
// model believe a setting is effective when Runtime actually discarded it.
func decodeClosedAuthoringJSON(payload json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("authoring payload contains trailing JSON value")
		}
		return err
	}
	return nil
}
