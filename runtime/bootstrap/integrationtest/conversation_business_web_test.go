package integrationtest

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationBusinessBrowserAndRestart(t *testing.T) {
	f := newBusinessWebFixture(t)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	var conversation agentsdk.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", map[string]any{"client_id": "browser-business", "title": "客户业务查询验收"}, 200).Body.Bytes(), &conversation); err != nil {
		t.Fatal(err)
	}
	run := b.run(conversation.ID)
	// Retain the public Run projection even if a live-model assertion fails.
	// The fixture contains only synthetic data and no service credentials.
	if dir := os.Getenv("RUNTIME_BUSINESS_EVIDENCE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(run, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "business-run.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	queries, gets, catalogs := 0, 0, 0
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.ErrorCode != "" {
				t.Fatalf("business call failed: tool=%s arguments=%s code=%s", call.Name, call.Arguments, call.ErrorCode)
			}
			switch call.Name {
			case "query_records":
				queries++
			case "get_record":
				gets++
			case "business_catalog":
				catalogs++
			}
		}
	}
	if queries < 3 || gets < 1 || catalogs < 1 {
		t.Fatalf("missing model discovery/cursor/detail sequence: queries=%d gets=%d catalogs=%d", queries, gets, catalogs)
	}
	text := run.Steps[len(run.Steps)-1].Text
	for _, word := range []string{"Acme", "Beta", "Gamma", "10", "20", "30"} {
		if !strings.Contains(text, word) {
			t.Fatalf("model answer omitted actual business fact %q", word)
		}
	}
	if strings.Contains(text, "Other") {
		t.Fatal("model disclosed another owner's customer")
	}
	path := "/agent/conversations/" + conversation.ID + "/messages"
	checkHistory := func(hidden bool) {
		t.Helper()
		var page agentsdk.ConversationMessagePage
		if err := json.Unmarshal(b.call("GET", path, nil, 200).Body.Bytes(), &page); err != nil || len(page.Items) != 2 {
			t.Fatal(page, err)
		}
		message := page.Items[1]
		if hidden {
			if message.AccessError == "" || strings.Contains(message.Content, "Acme") {
				t.Fatal("revoked business content remained available", message)
			}
		} else if message.AccessError != "" || !strings.Contains(message.Content, "Acme") {
			t.Fatal("readable business answer was lost", message)
		}
	}
	checkHistory(false)
	// Complete shutdown/reopen of both actual service databases, retaining the
	// original browser session and frozen conversation evidence.
	f.close()
	f.open()
	b.session()
	checkHistory(false)
	b.assign("business_field_restricted")
	b.session()
	checkHistory(true)
	b.assign("headquarters_admin")
	b.session()
	checkHistory(false)
	b.assign("business_restricted")
	b.session()
	checkHistory(true)
	b.assign("headquarters_admin")
	b.session()
	checkHistory(false)
	if dir := os.Getenv("RUNTIME_BUSINESS_EVIDENCE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		metadata, _ := json.MarshalIndent(map[string]any{"conversation_id": conversation.ID, "run_id": run.ID, "origin": businessWebOrigin, "model": f.options.ConversationModel, "queries": queries, "catalogs": catalogs, "gets": gets, "restart_verified": true, "field_revocation_verified": true, "business_revocation_verified": true}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "business-acceptance.json"), metadata, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("Business browser HTTP verified: conversation=%s, queries=%d, get=%d; full restart and field/read revocation passed", conversation.ID, queries, gets)
	if os.Getenv("RUNTIME_BUSINESS_BROWSER") == "1" {
		serveBusinessBrowserAcceptance(t, f, b, conversation.ID)
	}
}

type businessHandlerSlot struct{ handler http.Handler }

func serveBusinessBrowserAcceptance(t *testing.T, f *businessWebFixture, b *businessBrowser, conversationID string, extraControls ...map[string]func() any) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:8093")
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Pointer[businessHandlerSlot]
	active.Store(&businessHandlerSlot{handler: f.handler})
	done := make(chan struct{})
	var once sync.Once
	var control sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { active.Load().handler.ServeHTTP(w, r) })
	for name, role := range map[string]string{"restrict-fields": "business_field_restricted", "revoke": "business_restricted", "restore": "headquarters_admin"} {
		mux.HandleFunc("POST /__acceptance/"+name, func(w http.ResponseWriter, r *http.Request) {
			control.Lock()
			defer control.Unlock()
			b.assign(role)
			w.WriteHeader(204)
		})
	}
	mux.HandleFunc("POST /__acceptance/restart", func(w http.ResponseWriter, r *http.Request) {
		control.Lock()
		defer control.Unlock()
		active.Store(&businessHandlerSlot{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "Restarting acceptance host", 503) })})
		f.close()
		f.open()
		b.session()
		active.Store(&businessHandlerSlot{handler: f.handler})
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /__acceptance/finish", func(w http.ResponseWriter, r *http.Request) { once.Do(func() { close(done) }); w.WriteHeader(204) })
	for _, controls := range extraControls {
		for name, action := range controls {
			mux.HandleFunc("POST /__acceptance/"+name, func(w http.ResponseWriter, r *http.Request) {
				control.Lock()
				defer control.Unlock()
				result := action()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(result)
			})
		}
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	t.Logf("Browser acceptance ready: %s/#%s; synthetic login admin@example.com, password %s", businessWebOrigin, conversationID, businessWebPassword)
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("business browser acceptance cancelled")
	case <-time.After(20 * time.Minute):
		t.Fatal("business browser acceptance timed out")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
