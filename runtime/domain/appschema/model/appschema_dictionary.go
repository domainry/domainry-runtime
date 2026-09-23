package appschemamodel

import metadatasdk "github.com/domainry/domainry-metadata-sdk"

// Dictionary definitions are Metadata-owned. Runtime uses aliases only in its
// in-memory application schema view; project model JSON has no dictionary area.
type DictionarySchema = metadatasdk.Dictionary
type DictionaryItemSchema = metadatasdk.DictionaryItem
