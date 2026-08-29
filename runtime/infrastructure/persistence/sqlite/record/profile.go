package record

import "database/sql"

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) OrderedDecimalTextStorage() bool   { return true }
func (Profile) ReadIsolation() sql.IsolationLevel { return sql.LevelSerializable }
