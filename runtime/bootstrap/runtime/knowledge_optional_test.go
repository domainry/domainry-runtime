package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
	knowledgecontract "github.com/domainry/domainry-knowledge-sdk/contract"
	transportbootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/transport"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type scopedRetrievalFixture struct {
	knowledgecontract.Knowledge
	authority knowledgesdk.Authority
}

func (f *scopedRetrievalFixture) SearchLibraryKnowledge(_ context.Context, library, query string, a knowledgesdk.Authority) (knowledgesdk.Result, error) {
	f.authority = a
	return knowledgesdk.Result{LibraryID: library, Operation: "search", Query: query, ScopeSHA256: "scope"}, nil
}
func (f *scopedRetrievalFixture) ReadLibraryKnowledge(_ context.Context, library, document string, a knowledgesdk.Authority) (knowledgesdk.Result, error) {
	f.authority = a
	return knowledgesdk.Result{LibraryID: library, Operation: "fetch", DocumentID: document, ScopeSHA256: "scope"}, nil
}

func TestStandaloneRetrievalDoesNotOpenKnowledgeManagementOrSchema(t *testing.T) {
	store, err := prepareRuntimeStore(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source := &scopedRetrievalFixture{}
	owner, err := openRuntimeKnowledge(t.Context(), "runtime-a", identitysdk.ApplicationScope{}, store, nil, nil, nil, nil, knowledgecontract.Options{Knowledge: source})
	if err != nil || owner.binding != nil || owner.service != nil || owner.retrieval != source {
		t.Fatalf("retrieval installed a management owner: %+v error=%v", owner, err)
	}
	port := transportbootstrap.TaskKnowledgeSource(owner.retrieval)
	if port == nil {
		t.Fatal("standalone source was not usable by Agent tools")
	}
	a := knowledgesdk.Authority{Known: true, RuntimeID: "runtime-a", WorkspaceID: "workspace-a", UserID: "service-a", RoleKey: "agent-service"}
	result, err := port.ReadLibraryKnowledge(t.Context(), "kb-a", "doc-a", a)
	if err != nil || result.LibraryID != "kb-a" || !source.authority.Equal(a) {
		t.Fatalf("scope was lost: %+v %v", result, err)
	}
	// SQLite's schema catalogue has no Object/record equivalent. This read-only
	// dialect-specific assertion verifies that the absent owner created no tables.
	rows, err := store.DB().QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(name, "_knowledge_") {
			t.Fatal("retrieval created Knowledge management storage", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestStandaloneRetrievalRejectsModuleOptionsAndUnscopedFallback(t *testing.T) {
	source := &scopedRetrievalFixture{}
	for _, options := range []knowledgecontract.Options{
		{Knowledge: source.Knowledge}, // empty composition stays optional below
		{Knowledge: source, LibraryKnowledge: []knowledgecontract.LibraryKnowledgeBinding{{LibraryID: "kb-a"}}},
	} {
		owner, err := standaloneKnowledge(options)
		if options.Knowledge == nil {
			if err != nil || owner.retrieval != nil {
				t.Fatal("empty composition installed an owner", owner, err)
			}
		} else if err == nil {
			t.Fatal("module configuration was silently ignored", options)
		}
	}
	var typedNil *scopedRetrievalFixture
	if _, err := standaloneKnowledge(knowledgecontract.Options{Knowledge: typedNil}); err == nil {
		t.Fatal("nil source was accepted")
	}
	unscoped := &unscopedRetrievalFixture{}
	if _, err := standaloneKnowledge(knowledgecontract.Options{Knowledge: unscoped}); err == nil {
		t.Fatal("unscoped source was accepted")
	}
}

type unscopedRetrievalFixture struct{ knowledgecontract.Knowledge }
