package runtime

import (
	"context"
	"testing"
	"time"

	identity "github.com/domainry/domainry-identity-sdk/authorization"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type closeTrackingPrincipalCache struct{ closed bool }

func (*closeTrackingPrincipalCache) Get(context.Context, identityprincipal.CacheKey, time.Time) (identityprincipal.CacheEntry, bool, error) {
	return identityprincipal.CacheEntry{}, false, nil
}
func (*closeTrackingPrincipalCache) Set(context.Context, identityprincipal.CacheKey, identityprincipal.CacheEntry, time.Time) error {
	return nil
}
func (*closeTrackingPrincipalCache) Delete(context.Context, identityprincipal.CacheKey) error {
	return nil
}
func (*closeTrackingPrincipalCache) Invalidate(context.Context, identity.SubjectID, identity.WorkspaceID) error {
	return nil
}
func (cache *closeTrackingPrincipalCache) Close() error { cache.closed = true; return nil }

func TestRuntimeCloseClosesPrincipalCache(t *testing.T) {
	cache := &closeTrackingPrincipalCache{}
	runtime := &Runtime{cfg: config.Config{HTTPShutdownTimeout: time.Second}, borrowedStore: true, principalCache: cache}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !cache.closed {
		t.Fatal("principal cache was not closed")
	}
}
