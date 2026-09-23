package runtime

import (
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type subjectLifecyclePersistenceIdentityBinding struct {
	runtimeIdentityBindingStub
	mode      identitysdk.DeploymentMode
	bindErr   error
	bindCalls int
}

func (b *subjectLifecyclePersistenceIdentityBinding) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: b.mode}
}

func (b *subjectLifecyclePersistenceIdentityBinding) BindSubjectLifecyclePersistence() error {
	b.bindCalls++
	return b.bindErr
}

func TestBindEmbeddedIdentitySubjectLifecyclePersistence(t *testing.T) {
	binding := &subjectLifecyclePersistenceIdentityBinding{mode: identitysdk.DeploymentModeModule}
	if err := bindEmbeddedIdentitySubjectLifecyclePersistence(binding); err != nil {
		t.Fatalf("bind embedded Identity subject lifecycle persistence: %v", err)
	}
	if binding.bindCalls != 1 {
		t.Fatalf("bind calls = %d, want 1", binding.bindCalls)
	}
}

func TestBindEmbeddedIdentitySubjectLifecyclePersistenceSkipsSaaS(t *testing.T) {
	binding := &subjectLifecyclePersistenceIdentityBinding{mode: identitysdk.DeploymentModeSaaS}
	if err := bindEmbeddedIdentitySubjectLifecyclePersistence(binding); err != nil {
		t.Fatalf("skip SaaS Identity subject lifecycle persistence: %v", err)
	}
	if binding.bindCalls != 0 {
		t.Fatalf("bind calls = %d, want 0", binding.bindCalls)
	}
}

func TestBindEmbeddedIdentitySubjectLifecyclePersistenceRequiresBinder(t *testing.T) {
	err := bindEmbeddedIdentitySubjectLifecyclePersistence(runtimeIdentityBindingStub{})
	if err == nil || !strings.Contains(err.Error(), "no shared subject lifecycle persistence binder") {
		t.Fatalf("missing binder error = %v", err)
	}
}

func TestBindEmbeddedIdentitySubjectLifecyclePersistencePropagatesFailure(t *testing.T) {
	wantErr := errors.New("shared tables unavailable")
	binding := &subjectLifecyclePersistenceIdentityBinding{mode: identitysdk.DeploymentModeModule, bindErr: wantErr}
	err := bindEmbeddedIdentitySubjectLifecyclePersistence(binding)
	if !errors.Is(err, wantErr) {
		t.Fatalf("bind error = %v, want %v", err, wantErr)
	}
}
