// Package lifecyclesdkfixture opens the real Lifecycle module through its SDK
// boundary for Runtime integration tests.
package lifecyclesdkfixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	auditmoduleimpl "github.com/domainry/domainry-audit/module"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemoduleimpl "github.com/domainry/domainry-lifecycle/module"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodulehost "github.com/domainry/domainry-metadata-sdk/modulehost"
	metadatamodule "github.com/domainry/domainry-metadata/module"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclemodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func Open(ctx context.Context, store *database.RuntimeStore, runtimeID string) (lifecyclesdk.Binding, error) {
	if store == nil {
		return nil, fmt.Errorf("Lifecycle test store is required")
	}
	if store.Metadata() == nil {
		metadata, err := metadatamodule.NewFactory().OpenModule(ctx, metadatasdk.ApplicationRef{InstallationID: runtimeID}, metadataHost{store: store})
		if err != nil {
			return nil, fmt.Errorf("open shared Metadata module for Lifecycle test: %w", err)
		}
		if err := store.BindMetadata(metadata); err != nil {
			_ = metadata.Close(context.WithoutCancel(ctx))
			return nil, err
		}
	}
	content := newMemoryArtifactContent()
	auditBinding, err := auditmoduleimpl.NewFactory(auditmoduleimpl.Options{}).OpenModule(ctx, auditsdk.ApplicationRef{InstallationID: runtimeID}, runtimeauditmodule.NewHost(store, content, content))
	if err != nil {
		return nil, fmt.Errorf("open shared Audit module for Lifecycle test: %w", err)
	}
	binding, err := lifecyclemoduleimpl.NewFactory().OpenModule(ctx, lifecyclesdk.ApplicationRef{RuntimeID: runtimeID}, lifecyclemodule.NewHost(store, auditBinding, content))
	if err != nil {
		return nil, err
	}
	if err := binding.Descriptor().Validate(); err != nil {
		_ = binding.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return binding, nil
}

type memoryArtifactContent struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newMemoryArtifactContent() *memoryArtifactContent {
	return &memoryArtifactContent{values: map[string][]byte{}}
}

func (s *memoryArtifactContent) PutImmutable(_ context.Context, workspaceID, key string, content []byte) (lifecyclecontract.ArtifactContentInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := workspaceID + "\x00" + key
	if existing, found := s.values[identity]; found && !bytes.Equal(existing, content) {
		return lifecyclecontract.ArtifactContentInfo{}, fmt.Errorf("artifact content identity conflict")
	}
	s.values[identity] = append([]byte(nil), content...)
	digest := sha256.Sum256(content)
	return lifecyclecontract.ArtifactContentInfo{Reference: key, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}, nil
}

func (s *memoryArtifactContent) Open(_ context.Context, workspaceID, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, found := s.values[workspaceID+"\x00"+key]
	if !found {
		return nil, lifecyclecontract.ErrArtifactContentNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), content...))), nil
}

func (s *memoryArtifactContent) Stat(_ context.Context, workspaceID, key string) (lifecyclecontract.ArtifactContentInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, found := s.values[workspaceID+"\x00"+key]
	if !found {
		return lifecyclecontract.ArtifactContentInfo{}, lifecyclecontract.ErrArtifactContentNotFound
	}
	digest := sha256.Sum256(content)
	return lifecyclecontract.ArtifactContentInfo{Reference: key, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}, nil
}

func (s *memoryArtifactContent) Delete(_ context.Context, workspaceID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, workspaceID+"\x00"+key)
	return nil
}

type metadataHost struct{ store *database.RuntimeStore }

func (h metadataHost) Database() metadatamodulehost.Database { return h.store.DB() }
func (h metadataHost) Dialect() metadatamodulehost.Dialect   { return h.store.RuntimeRenderer() }
func (h metadataHost) Migrations() metadatamodulehost.MigrationRegistrar {
	return metadataMigrationRegistrar{store: h.store}
}

type metadataMigrationRegistrar struct{ store *database.RuntimeStore }

func (r metadataMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r metadataMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }
func (r metadataMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, values []metadatamodulehost.SchemaMigration) error {
	return r.store.ApplyORMOwnedMigrations(ctx, owner, values)
}
