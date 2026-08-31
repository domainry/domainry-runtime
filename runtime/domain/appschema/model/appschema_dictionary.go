package appschemamodel

import metadatasdk "github.com/domainry/domainry-metadata-sdk"

// Dictionary definitions are Metadata-owned; Runtime keeps source-compatible
// aliases because manifests and Runtime schema projections still reference
// these names.
type DictionarySchema = metadatasdk.Dictionary
type DictionaryItemSchema = metadatasdk.DictionaryItem
