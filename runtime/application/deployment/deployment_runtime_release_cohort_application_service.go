package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
)

const (
	defaultRuntimeReleaseLeaseDuration = 45 * time.Second
	defaultRuntimeReleaseHeartbeat     = 15 * time.Second
)

type DeploymentRuntimeReleaseCohortApplicationService struct {
	repository    deploymentrepository.RuntimeReleaseCohortRepository
	leaseDuration time.Duration
	heartbeat     time.Duration
}

func NewDeploymentRuntimeReleaseCohortApplicationService(repository deploymentrepository.RuntimeReleaseCohortRepository) *DeploymentRuntimeReleaseCohortApplicationService {
	return &DeploymentRuntimeReleaseCohortApplicationService{repository: repository, leaseDuration: defaultRuntimeReleaseLeaseDuration, heartbeat: defaultRuntimeReleaseHeartbeat}
}

func (s *DeploymentRuntimeReleaseCohortApplicationService) Join(ctx context.Context, instanceID string, identity deploymentmodel.RuntimeReleaseIdentity, now time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	if !identity.Coordinated() {
		return deploymentmodel.RuntimeReleaseCohortLease{}, nil
	}
	if s == nil || s.repository == nil {
		return deploymentmodel.RuntimeReleaseCohortLease{}, fmt.Errorf("%w: release cohort repository is unavailable", deploymentmodel.ErrRuntimeReleaseAdmission)
	}
	if strings.TrimSpace(instanceID) == "" || strings.TrimSpace(instanceID) != instanceID {
		return deploymentmodel.RuntimeReleaseCohortLease{}, fmt.Errorf("%w: Runtime instance ID is malformed", deploymentmodel.ErrRuntimeReleaseAdmission)
	}
	if err := ValidateRuntimeReleaseIdentity(identity); err != nil {
		return deploymentmodel.RuntimeReleaseCohortLease{}, err
	}
	return s.repository.ClaimRuntimeRelease(ctx, deploymentmodel.RuntimeReleaseCohortClaim{InstanceID: instanceID, Identity: identity, Now: now.UTC(), LeaseDuration: s.leaseDuration})
}

func (s *DeploymentRuntimeReleaseCohortApplicationService) Heartbeat(ctx context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, now time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	if lease.InstanceID == "" {
		return lease, nil
	}
	return s.repository.HeartbeatRuntimeRelease(ctx, lease, now.UTC(), s.leaseDuration)
}

func (s *DeploymentRuntimeReleaseCohortApplicationService) Leave(ctx context.Context, lease deploymentmodel.RuntimeReleaseCohortLease) error {
	if lease.InstanceID == "" || s == nil || s.repository == nil {
		return nil
	}
	return s.repository.ReleaseRuntimeRelease(ctx, lease)
}

func (s *DeploymentRuntimeReleaseCohortApplicationService) HeartbeatInterval() time.Duration {
	if s == nil || s.heartbeat <= 0 {
		return defaultRuntimeReleaseHeartbeat
	}
	return s.heartbeat
}

func ValidateRuntimeReleaseIdentity(identity deploymentmodel.RuntimeReleaseIdentity) error {
	if identity.ContractVersion != deploymentmodel.RuntimeReleaseIdentityVersion {
		return fmt.Errorf("%w: release identity contract=%q", deploymentmodel.ErrRuntimeReleaseAdmission, identity.ContractVersion)
	}
	if identity.BuildMode != "development" && identity.BuildMode != "packaged" {
		return fmt.Errorf("%w: release build mode=%q", deploymentmodel.ErrRuntimeReleaseAdmission, identity.BuildMode)
	}
	for name, value := range map[string]string{
		"runtime version":              identity.RuntimeVersion,
		"runtimeext contract version":  identity.RuntimeextContractVersion,
		"connector contract version":   identity.ConnectorContractVersion,
		"domain SDK contract version":  identity.DomainSDKContractVersion,
		"domain SDK generator version": identity.DomainSDKGeneratorVersion,
		"domain SDK build constraint":  identity.DomainSDKBuildConstraint,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("%w: %s is malformed", deploymentmodel.ErrRuntimeReleaseAdmission, name)
		}
	}
	for name, value := range map[string]string{
		"runtimeext contract": identity.RuntimeextContractSHA256,
		"connector contract":  identity.ConnectorContractSHA256,
		"domain SDK contract": identity.DomainSDKContractSHA256,
		"metadata snapshot":   identity.MetadataSnapshotSHA256,
		"generated SDK":       identity.GeneratedSDKSHA256,
		"handler registry":    identity.HandlerRegistrySHA256,
		"connector registry":  identity.ConnectorRegistrySHA256,
		"combination":         identity.CombinationSHA256,
	} {
		if !runtimeReleaseLowerSHA256(value) {
			return fmt.Errorf("%w: %s SHA-256 is malformed", deploymentmodel.ErrRuntimeReleaseAdmission, name)
		}
	}
	want, _ := RuntimeReleaseCombinationSHA256(identity)
	if identity.CombinationSHA256 != want {
		return fmt.Errorf("%w: release combination SHA-256 does not match component identities", deploymentmodel.ErrRuntimeReleaseAdmission)
	}
	return nil
}

func RuntimeReleaseCombinationSHA256(identity deploymentmodel.RuntimeReleaseIdentity) (string, error) {
	identity.CombinationSHA256 = ""
	payload, _ := json.Marshal(identity)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func runtimeReleaseLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

// RuntimeReleaseAdmission is shared by the heartbeat owner and HTTP adapter.
// Once failed it is irreversible for the process: a process that lost its
// cohort lease must restart and rejoin instead of silently reopening traffic.
type RuntimeReleaseAdmission struct {
	mu     sync.RWMutex
	failed error
}

func (a *RuntimeReleaseAdmission) Fail(err error) {
	if a == nil || err == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed == nil {
		a.failed = fmt.Errorf("%w: %v", deploymentmodel.ErrRuntimeReleaseAdmission, err)
	}
}

func (a *RuntimeReleaseAdmission) Check() error {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.failed
}

var (
	ErrRuntimeReleaseBuildIntegrity     = errors.New("runtime release build integrity failed")
	ErrRuntimeReleaseSignatureIntegrity = errors.New("runtime release signature integrity failed")
	ErrRuntimeReleaseSchemaIntegrity    = errors.New("runtime release schema integrity failed")
	ErrRuntimeReleaseRegistryIntegrity  = errors.New("runtime release registry integrity failed")
)

type RuntimeReleaseArtifactEvidence struct {
	Verified       bool
	BuildError     error
	SignatureError error
}

// RuntimeReleaseIntegrity keeps immutable package evidence and continuously
// compares mutable Schema and Registry facts used by a project Runtime.
type RuntimeReleaseIntegrity struct {
	identity               deploymentmodel.RuntimeReleaseIdentity
	artifact               RuntimeReleaseArtifactEvidence
	expectedSchemaRevision string
	schemaRevision         func(context.Context) (string, error)
	handlers               *runtimeext.BusinessHandlerRegistry
	connectors             *connector.Registry
}

func NewRuntimeReleaseIntegrity(
	identity deploymentmodel.RuntimeReleaseIdentity,
	artifact RuntimeReleaseArtifactEvidence,
	expectedSchemaRevision string,
	schemaRevision func(context.Context) (string, error),
	handlers *runtimeext.BusinessHandlerRegistry,
	connectors *connector.Registry,
) *RuntimeReleaseIntegrity {
	return &RuntimeReleaseIntegrity{
		identity: identity, artifact: artifact,
		expectedSchemaRevision: expectedSchemaRevision, schemaRevision: schemaRevision,
		handlers: handlers, connectors: connectors,
	}
}

func (s *RuntimeReleaseIntegrity) BuildReadiness(context.Context) error {
	if s == nil || !s.identity.Coordinated() || s.identity.BuildMode == "development" {
		return nil
	}
	if !s.artifact.Verified {
		return fmt.Errorf("%w: runtime artifact evidence is unavailable", ErrRuntimeReleaseBuildIntegrity)
	}
	if s.artifact.BuildError != nil {
		return fmt.Errorf("%w: %v", ErrRuntimeReleaseBuildIntegrity, s.artifact.BuildError)
	}
	return nil
}

func (s *RuntimeReleaseIntegrity) SignatureReadiness(context.Context) error {
	if s == nil || !s.identity.Coordinated() || s.identity.BuildMode == "development" {
		return nil
	}
	if !s.artifact.Verified {
		return fmt.Errorf("%w: runtime artifact evidence is unavailable", ErrRuntimeReleaseSignatureIntegrity)
	}
	if s.artifact.SignatureError != nil {
		return fmt.Errorf("%w: %v", ErrRuntimeReleaseSignatureIntegrity, s.artifact.SignatureError)
	}
	return nil
}

func (s *RuntimeReleaseIntegrity) SchemaReadiness(ctx context.Context) error {
	if s == nil || !s.identity.Coordinated() {
		return nil
	}
	if s.schemaRevision == nil || s.expectedSchemaRevision == "" {
		return fmt.Errorf("%w: expected Runtime schema revision is unavailable", ErrRuntimeReleaseSchemaIntegrity)
	}
	current, err := s.schemaRevision(ctx)
	if err != nil {
		return fmt.Errorf("%w: load current Runtime schema revision: %v", ErrRuntimeReleaseSchemaIntegrity, err)
	}
	if current == "" || current != s.expectedSchemaRevision {
		return fmt.Errorf("%w: expected=%q current=%q", ErrRuntimeReleaseSchemaIntegrity, s.expectedSchemaRevision, current)
	}
	return nil
}

func (s *RuntimeReleaseIntegrity) RegistryReadiness(context.Context) error {
	if s == nil || !s.identity.Coordinated() {
		return nil
	}
	if s.handlers == nil || !s.handlers.Frozen() || s.connectors == nil || !s.connectors.Frozen() {
		return fmt.Errorf("%w: project registries are unavailable or mutable", ErrRuntimeReleaseRegistryIntegrity)
	}
	handlerHash, _ := deploymentmodel.RuntimeRegistrySHA256("domainry-handler-registry-v1", s.handlers.Descriptors())
	connectorHash, _ := deploymentmodel.RuntimeRegistrySHA256("domainry-connector-registry-v1", s.connectors.Descriptors())
	if handlerHash != s.identity.HandlerRegistrySHA256 || connectorHash != s.identity.ConnectorRegistrySHA256 {
		return fmt.Errorf("%w: Handler or Connector Registry differs from release identity", ErrRuntimeReleaseRegistryIntegrity)
	}
	return nil
}
