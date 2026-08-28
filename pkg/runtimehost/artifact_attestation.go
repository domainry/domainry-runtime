package runtimehost

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

const RuntimeArtifactAttestationVersion = "domainry-runtime-artifact-attestation-v2"

var ErrRuntimeArtifactAttestation = errors.New("runtime artifact attestation is invalid")

// RuntimeArtifactAttestation is the signed sidecar shipped beside a packaged
// Runtime executable. Its signature covers the executable digest and all
// project build identities without creating a self-referential binary.
type RuntimeArtifactAttestation struct {
	ContractVersion      string                      `json:"contract_version"`
	ProjectModule        string                      `json:"project_module"`
	ReceiptSHA256        string                      `json:"receipt_sha256"`
	ProjectInputSHA256   string                      `json:"project_input_sha256"`
	ProjectSourceSHA256  string                      `json:"project_source_sha256"`
	GeneratedSDKSHA256   string                      `json:"generated_sdk_sha256"`
	HandlerCatalogSHA256 string                      `json:"handler_catalog_sha256"`
	FrontendBundleSHA256 string                      `json:"frontend_bundle_sha256,omitempty"`
	BinarySHA256         string                      `json:"binary_sha256"`
	BinarySizeBytes      int64                       `json:"binary_size_bytes"`
	SigningKeyID         string                      `json:"signing_key_id"`
	SigningPublicKey     string                      `json:"signing_public_key_base64"`
	Signature            RuntimeAttestationSignature `json:"signature"`
}

type RuntimeAttestationSignature struct {
	Algorithm   string `json:"algorithm"`
	ValueBase64 string `json:"value_base64"`
}

type runtimeArtifactAttestationPayload struct {
	ContractVersion      string `json:"contract_version"`
	ProjectModule        string `json:"project_module"`
	ReceiptSHA256        string `json:"receipt_sha256"`
	ProjectInputSHA256   string `json:"project_input_sha256"`
	ProjectSourceSHA256  string `json:"project_source_sha256"`
	GeneratedSDKSHA256   string `json:"generated_sdk_sha256"`
	HandlerCatalogSHA256 string `json:"handler_catalog_sha256"`
	FrontendBundleSHA256 string `json:"frontend_bundle_sha256,omitempty"`
	BinarySHA256         string `json:"binary_sha256"`
	BinarySizeBytes      int64  `json:"binary_size_bytes"`
	SigningKeyID         string `json:"signing_key_id"`
	SigningPublicKey     string `json:"signing_public_key_base64"`
}

// CanonicalPayload returns the exact bytes signed by Builder and verified
// by runtimehost.
func (a RuntimeArtifactAttestation) CanonicalPayload() ([]byte, error) {
	return json.Marshal(runtimeArtifactAttestationPayload{
		ContractVersion: a.ContractVersion, ProjectModule: a.ProjectModule,
		ReceiptSHA256: a.ReceiptSHA256, ProjectInputSHA256: a.ProjectInputSHA256,
		ProjectSourceSHA256: a.ProjectSourceSHA256, GeneratedSDKSHA256: a.GeneratedSDKSHA256,
		HandlerCatalogSHA256: a.HandlerCatalogSHA256, FrontendBundleSHA256: a.FrontendBundleSHA256,
		BinarySHA256:    a.BinarySHA256,
		BinarySizeBytes: a.BinarySizeBytes, SigningKeyID: a.SigningKeyID,
		SigningPublicKey: a.SigningPublicKey,
	})
}

func signRuntimeArtifactAttestation(attestation RuntimeArtifactAttestation, privateKey ed25519.PrivateKey) (RuntimeArtifactAttestation, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return RuntimeArtifactAttestation{}, fmt.Errorf("%w: Ed25519 private key is malformed", ErrRuntimeArtifactAttestation)
	}
	if err := validateRuntimeArtifactAttestationPayload(attestation); err != nil {
		return RuntimeArtifactAttestation{}, err
	}
	payload, err := attestation.CanonicalPayload()
	if err != nil {
		return RuntimeArtifactAttestation{}, fmt.Errorf("%w: encode payload: %v", ErrRuntimeArtifactAttestation, err)
	}
	attestation.Signature = RuntimeAttestationSignature{
		Algorithm:   "ed25519",
		ValueBase64: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload)),
	}
	return attestation, nil
}

func decodeRuntimeArtifactAttestation(content []byte) (RuntimeArtifactAttestation, error) {
	if err := rejectDuplicateAttestationProperties(content); err != nil {
		return RuntimeArtifactAttestation{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var attestation RuntimeArtifactAttestation
	if err := decoder.Decode(&attestation); err != nil {
		return RuntimeArtifactAttestation{}, fmt.Errorf("%w: decode: %v", ErrRuntimeArtifactAttestation, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return RuntimeArtifactAttestation{}, fmt.Errorf("%w: expected one JSON document", ErrRuntimeArtifactAttestation)
	}
	return attestation, nil
}

// DecodeRuntimeArtifactAttestation strictly decodes one signed sidecar.
func DecodeRuntimeArtifactAttestation(content []byte) (RuntimeArtifactAttestation, error) {
	return decodeRuntimeArtifactAttestation(content)
}

func validateRuntimeArtifactAttestationPayload(attestation RuntimeArtifactAttestation) error {
	if attestation.ContractVersion != RuntimeArtifactAttestationVersion {
		return fmt.Errorf("%w: contract version %q", ErrRuntimeArtifactAttestation, attestation.ContractVersion)
	}
	if strings.TrimSpace(attestation.ProjectModule) != attestation.ProjectModule || attestation.ProjectModule == "" ||
		strings.TrimSpace(attestation.SigningKeyID) != attestation.SigningKeyID || attestation.SigningKeyID == "" {
		return fmt.Errorf("%w: project module or signing key identity is malformed", ErrRuntimeArtifactAttestation)
	}
	for name, value := range map[string]string{
		"receipt": attestation.ReceiptSHA256, "project input": attestation.ProjectInputSHA256,
		"project source": attestation.ProjectSourceSHA256, "generated SDK": attestation.GeneratedSDKSHA256,
		"handler catalog": attestation.HandlerCatalogSHA256, "binary": attestation.BinarySHA256,
	} {
		if !lowerSHA256(value) {
			return fmt.Errorf("%w: %s SHA-256 is malformed", ErrRuntimeArtifactAttestation, name)
		}
	}
	if attestation.FrontendBundleSHA256 != "" && !lowerSHA256(attestation.FrontendBundleSHA256) {
		return fmt.Errorf("%w: frontend bundle SHA-256 is malformed", ErrRuntimeArtifactAttestation)
	}
	if attestation.BinarySizeBytes <= 0 {
		return fmt.Errorf("%w: binary size is malformed", ErrRuntimeArtifactAttestation)
	}
	publicKey, err := base64.StdEncoding.DecodeString(attestation.SigningPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(publicKey) != attestation.SigningPublicKey {
		return fmt.Errorf("%w: signing public key is malformed", ErrRuntimeArtifactAttestation)
	}
	return nil
}

type runtimeArtifactEvidence struct {
	BuildError           error
	SignatureError       error
	FrontendBundleSHA256 string
}

func verifyRuntimeArtifact(executablePath string, identity deploymentmodel.RuntimeReleaseIdentity, readFile func(string) ([]byte, error)) runtimeArtifactEvidence {
	if identity.BuildMode != "packaged" {
		return runtimeArtifactEvidence{}
	}
	if readFile == nil {
		readFile = os.ReadFile
	}
	attestationPath := executablePath + ".attestation.json"
	content, err := readFile(attestationPath)
	if err != nil {
		wrapped := fmt.Errorf("%w: read %s: %v", ErrRuntimeArtifactAttestation, attestationPath, err)
		return runtimeArtifactEvidence{BuildError: wrapped, SignatureError: wrapped}
	}
	attestation, err := decodeRuntimeArtifactAttestation(content)
	if err != nil {
		return runtimeArtifactEvidence{BuildError: err, SignatureError: err}
	}
	buildErr := validateRuntimeArtifactBuild(executablePath, identity, attestation, readFile)
	signatureErr := validateRuntimeArtifactSignature(identity, attestation)
	return runtimeArtifactEvidence{
		BuildError: buildErr, SignatureError: signatureErr,
		FrontendBundleSHA256: attestation.FrontendBundleSHA256,
	}
}

func validateRuntimeArtifactBuild(executablePath string, identity deploymentmodel.RuntimeReleaseIdentity, attestation RuntimeArtifactAttestation, readFile func(string) ([]byte, error)) error {
	if err := validateRuntimeArtifactAttestationPayload(attestation); err != nil {
		return err
	}
	for _, field := range []struct{ name, actual, expected string }{
		{"project module", attestation.ProjectModule, identity.ProjectModule},
		{"receipt", attestation.ReceiptSHA256, identity.VerificationReceiptSHA256},
		{"project input", attestation.ProjectInputSHA256, identity.ProjectInputSHA256},
		{"project source", attestation.ProjectSourceSHA256, identity.ProjectSourceSHA256},
		{"generated SDK", attestation.GeneratedSDKSHA256, identity.GeneratedSDKSHA256},
		{"handler catalog", attestation.HandlerCatalogSHA256, identity.HandlerCatalogSHA256},
		{"signing key ID", attestation.SigningKeyID, identity.SigningKeyID},
	} {
		if field.actual != field.expected {
			return fmt.Errorf("%w: %s differs from compiled identity", ErrRuntimeArtifactAttestation, field.name)
		}
	}
	binary, err := readFile(executablePath)
	if err != nil {
		return fmt.Errorf("%w: read executable: %v", ErrRuntimeArtifactAttestation, err)
	}
	digest := sha256.Sum256(binary)
	if int64(len(binary)) != attestation.BinarySizeBytes || hex.EncodeToString(digest[:]) != attestation.BinarySHA256 {
		return fmt.Errorf("%w: executable checksum or size differs", ErrRuntimeArtifactAttestation)
	}
	return nil
}

func validateRuntimeArtifactSignature(identity deploymentmodel.RuntimeReleaseIdentity, attestation RuntimeArtifactAttestation) error {
	if err := validateRuntimeArtifactAttestationPayload(attestation); err != nil {
		return err
	}
	publicKey, _ := base64.StdEncoding.DecodeString(attestation.SigningPublicKey)
	publicKeyDigest := sha256.Sum256(publicKey)
	if hex.EncodeToString(publicKeyDigest[:]) != identity.SigningPublicKeySHA256 {
		return fmt.Errorf("%w: signing public key differs from compiled identity", ErrRuntimeArtifactAttestation)
	}
	if attestation.Signature.Algorithm != "ed25519" {
		return fmt.Errorf("%w: signature algorithm %q", ErrRuntimeArtifactAttestation, attestation.Signature.Algorithm)
	}
	signature, err := base64.StdEncoding.DecodeString(attestation.Signature.ValueBase64)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(signature) != attestation.Signature.ValueBase64 {
		return fmt.Errorf("%w: signature encoding is malformed", ErrRuntimeArtifactAttestation)
	}
	payload, err := attestation.CanonicalPayload()
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return fmt.Errorf("%w: Ed25519 signature verification failed", ErrRuntimeArtifactAttestation)
	}
	return nil
}

func rejectDuplicateAttestationProperties(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var inspect func(string) error
	inspect = func(location string) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				raw, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := raw.(string)
				if !ok {
					return fmt.Errorf("%s has a non-string property", location)
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("%w: duplicate property %s.%s", ErrRuntimeArtifactAttestation, location, key)
				}
				seen[key] = struct{}{}
				if err := inspect(location + "." + key); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for index := 0; decoder.More(); index++ {
				if err := inspect(fmt.Sprintf("%s[%d]", location, index)); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return nil
		}
	}
	if err := inspect("$"); err != nil {
		if errors.Is(err, ErrRuntimeArtifactAttestation) {
			return err
		}
		return fmt.Errorf("%w: inspect JSON: %v", ErrRuntimeArtifactAttestation, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: expected one JSON document", ErrRuntimeArtifactAttestation)
	}
	return nil
}
