package record

import "database/sql"

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) OrderedDecimalTextStorage() bool   { return false }
func (Profile) ReadIsolation() sql.IsolationLevel { return sql.LevelSerializable }
