package businessseedmodel

import (
	"bytes"
	"encoding/json"
)

// UnmarshalJSON preserves source numbers before manifests are hashed or seed
// rows are materialized. Only the open record payload uses json.Number; the
// enclosing seed contract remains strict.
func (s *SeedRecordSchema) UnmarshalJSON(raw []byte) error {
	type seed SeedRecordSchema
	var decoded seed
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*s = SeedRecordSchema(decoded)
	return nil
}
