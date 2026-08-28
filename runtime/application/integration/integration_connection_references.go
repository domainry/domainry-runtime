package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ConnectionReferenceResource struct {
	Kind  string
	Key   string
	Value any
}

// FindConnectionReferences owns reference discovery and deterministic ordering.
// Composition only supplies the cross-domain resources that are visible to it.
func FindConnectionReferences(ctx context.Context, resources []ConnectionReferenceResource, connectionKey string) ([]ConnectionReference, error) {
	references := []ConnectionReference{}
	for _, resource := range resources {
		paths, err := FindConnectionReferencePaths(ctx, resource.Value, connectionKey)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			references = append(references, ConnectionReference{Kind: resource.Kind, Key: resource.Key, Path: path})
		}
	}
	sort.Slice(references, func(i, j int) bool {
		if references[i].Kind != references[j].Kind {
			return references[i].Kind < references[j].Kind
		}
		if references[i].Key != references[j].Key {
			return references[i].Key < references[j].Key
		}
		return references[i].Path < references[j].Path
	})
	return references, nil
}

// FindConnectionReferencePaths returns every serialized connection_key field
// that refers to connectionKey. The caller remains responsible for choosing
// which domain resources are searched.
func FindConnectionReferencePaths(ctx context.Context, value any, connectionKey string) ([]string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return []string{"encoding_error"}, nil
	}
	var decoded any
	_ = json.Unmarshal(raw, &decoded) // json.Marshal above guarantees valid JSON.
	return findConnectionReferencePaths(ctx, decoded, connectionKey, "")
}

func findConnectionReferencePaths(ctx context.Context, value any, connectionKey, path string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths := []string{}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if key == "connection_key" && strings.TrimSpace(fmt.Sprint(child)) == connectionKey {
				paths = append(paths, childPath)
			}
			childPaths, err := findConnectionReferencePaths(ctx, child, connectionKey, childPath)
			if err != nil {
				return nil, err
			}
			paths = append(paths, childPaths...)
		}
	case []any:
		for index, child := range typed {
			childPaths, err := findConnectionReferencePaths(ctx, child, connectionKey, fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return nil, err
			}
			paths = append(paths, childPaths...)
		}
	}
	return paths, nil
}
