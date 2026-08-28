package locales

import "embed"

// Files makes the Runtime backend locale catalog part of the release binary.
//
//go:embed *.toml
var Files embed.FS
