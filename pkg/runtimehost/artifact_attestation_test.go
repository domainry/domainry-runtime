package runtimehost

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

func TestRuntimeArtifactAttestationVerifiesExecutableBuildAndSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("packaged-runtime-binary")
	binaryDigest := sha256.Sum256(binary)
	publicKeyDigest := sha256.Sum256(publicKey)
	identity := deploymentmodel.RuntimeReleaseIdentity{
		BuildMode: "packaged", ProjectModule: "example.com/project",
		VerificationReceiptSHA256: strings.Repeat("1", 64),
		ProjectInputSHA256:        strings.Repeat("2", 64),
		ProjectSourceSHA256:       strings.Repeat("3", 64),
		GeneratedSDKSHA256:        strings.Repeat("4", 64),
		HandlerCatalogSHA256:      strings.Repeat("5", 64),
		SigningKeyID:              "project-release",
		SigningPublicKeySHA256:    hex.EncodeToString(publicKeyDigest[:]),
	}
	attestation, err := signRuntimeArtifactAttestation(RuntimeArtifactAttestation{
		ContractVersion: RuntimeArtifactAttestationVersion,
		ProjectModule:   identity.ProjectModule, ReceiptSHA256: identity.VerificationReceiptSHA256,
		ProjectInputSHA256: identity.ProjectInputSHA256, ProjectSourceSHA256: identity.ProjectSourceSHA256,
		GeneratedSDKSHA256: identity.GeneratedSDKSHA256, HandlerCatalogSHA256: identity.HandlerCatalogSHA256,
		BinarySHA256: hex.EncodeToString(binaryDigest[:]), BinarySizeBytes: int64(len(binary)),
		SigningKeyID: identity.SigningKeyID, SigningPublicKey: base64.StdEncoding.EncodeToString(publicKey),
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	attestationBytes, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"/app/runtime": binary, "/app/runtime.attestation.json": attestationBytes}
	read := func(path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, errors.New("missing")
		}
		return content, nil
	}
	evidence := verifyRuntimeArtifact("/app/runtime", identity, read)
	if evidence.BuildError != nil || evidence.SignatureError != nil {
		t.Fatalf("evidence=%+v", evidence)
	}

	driftedIdentity := identity
	driftedIdentity.ProjectSourceSHA256 = strings.Repeat("9", 64)
	evidence = verifyRuntimeArtifact("/app/runtime", driftedIdentity, read)
	if evidence.BuildError == nil || evidence.SignatureError != nil {
		t.Fatalf("drifted identity evidence=%+v", evidence)
	}

	files["/app/runtime"] = []byte("tampered")
	evidence = verifyRuntimeArtifact("/app/runtime", identity, read)
	if evidence.BuildError == nil || evidence.SignatureError != nil {
		t.Fatalf("tampered binary evidence=%+v", evidence)
	}
	files["/app/runtime"] = binary
	attestation.Signature.ValueBase64 = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	files["/app/runtime.attestation.json"], _ = json.Marshal(attestation)
	evidence = verifyRuntimeArtifact("/app/runtime", identity, read)
	if evidence.BuildError != nil || evidence.SignatureError == nil {
		t.Fatalf("tampered signature evidence=%+v", evidence)
	}
}

func TestRuntimeArtifactAttestationRejectsMissingDuplicateAndDevelopmentBypasses(t *testing.T) {
	identity := deploymentmodel.RuntimeReleaseIdentity{BuildMode: "packaged"}
	missing := verifyRuntimeArtifact("/app/runtime", identity, func(string) ([]byte, error) { return nil, errors.New("missing") })
	if missing.BuildError == nil || missing.SignatureError == nil {
		t.Fatalf("missing evidence=%+v", missing)
	}
	duplicate := []byte(`{"contract_version":"a","contract_version":"b"}`)
	files := map[string][]byte{"/app/runtime": []byte("runtime"), "/app/runtime.attestation.json": duplicate}
	evidence := verifyRuntimeArtifact("/app/runtime", identity, func(path string) ([]byte, error) { return files[path], nil })
	if evidence.BuildError == nil || evidence.SignatureError == nil {
		t.Fatalf("duplicate evidence=%+v", evidence)
	}
	unknown := []byte(`{"contract_version":"a","unknown":true}`)
	files["/app/runtime.attestation.json"] = unknown
	evidence = verifyRuntimeArtifact("/app/runtime", identity, func(path string) ([]byte, error) { return files[path], nil })
	if evidence.BuildError == nil || evidence.SignatureError == nil {
		t.Fatalf("unknown property evidence=%+v", evidence)
	}
	development := verifyRuntimeArtifact("/does/not/exist", deploymentmodel.RuntimeReleaseIdentity{BuildMode: "development"}, nil)
	if development.BuildError != nil || development.SignatureError != nil {
		t.Fatalf("development evidence=%+v", development)
	}
}
