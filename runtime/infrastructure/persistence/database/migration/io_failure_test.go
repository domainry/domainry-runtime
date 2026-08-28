package migration

import (
	"bytes"
	"crypto/cipher"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var errInjectedMigrationIO = errors.New("injected migration I/O failure")

type migrationErrorReadCloser struct{}

func (migrationErrorReadCloser) Read([]byte) (int, error) { return 0, errInjectedMigrationIO }
func (migrationErrorReadCloser) Close() error             { return nil }

type migrationBackupOutput struct {
	bytes.Buffer
	writeErr error
	syncErr  error
	closeErr error
}

func (output *migrationBackupOutput) Write(value []byte) (int, error) {
	if output.writeErr != nil {
		return 0, output.writeErr
	}
	return output.Buffer.Write(value)
}
func (output *migrationBackupOutput) Sync() error  { return output.syncErr }
func (output *migrationBackupOutput) Close() error { return output.closeErr }

type migrationBufferedWriter struct {
	calls    int
	failAt   int
	flushErr error
}

func (writer *migrationBufferedWriter) Write(value []byte) (int, error) {
	writer.calls++
	if writer.calls == writer.failAt {
		return 0, errInjectedMigrationIO
	}
	return len(value), nil
}
func (writer *migrationBufferedWriter) Flush() error { return writer.flushErr }

func TestBackupCryptoPropagatesEveryEncryptFailureStage(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{1}, 32)

	t.Run("aead", func(t *testing.T) {
		original := newBackupAEAD
		newBackupAEAD = func(cipher.Block) (cipher.AEAD, error) { return nil, errInjectedMigrationIO }
		defer func() { newBackupAEAD = original }()
		if err := EncryptBackupFile(source, filepath.Join(dir, "aead"), key); !errors.Is(err, errInjectedMigrationIO) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("nonce", func(t *testing.T) {
		originalOutput, originalNonce := openBackupOutput, readBackupNonce
		openBackupOutput = func(string) (backupOutput, error) { return &migrationBackupOutput{}, nil }
		readBackupNonce = func([]byte) (int, error) { return 0, errInjectedMigrationIO }
		defer func() { openBackupOutput, readBackupNonce = originalOutput, originalNonce }()
		if err := EncryptBackupFile(source, filepath.Join(dir, "nonce"), key); !errors.Is(err, errInjectedMigrationIO) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("input-read", func(t *testing.T) {
		originalInput, originalOutput := openBackupInput, openBackupOutput
		openBackupInput = func(string) (io.ReadCloser, error) { return migrationErrorReadCloser{}, nil }
		openBackupOutput = func(string) (backupOutput, error) { return &migrationBackupOutput{}, nil }
		defer func() { openBackupInput, openBackupOutput = originalInput, originalOutput }()
		if err := EncryptBackupFile(source, filepath.Join(dir, "read"), key); !errors.Is(err, errInjectedMigrationIO) {
			t.Fatalf("error=%v", err)
		}
	})

	for _, test := range []struct {
		name     string
		failAt   int
		flushErr error
	}{
		{name: "magic-write", failAt: 1},
		{name: "nonce-write", failAt: 2},
		{name: "length-write", failAt: 3},
		{name: "chunk-write", failAt: 4},
		{name: "flush", flushErr: errInjectedMigrationIO},
	} {
		t.Run(test.name, func(t *testing.T) {
			originalOutput, originalWriter := openBackupOutput, newBackupBufferedWriter
			openBackupOutput = func(string) (backupOutput, error) { return &migrationBackupOutput{}, nil }
			newBackupBufferedWriter = func(io.Writer) backupBufferedWriter {
				return &migrationBufferedWriter{failAt: test.failAt, flushErr: test.flushErr}
			}
			defer func() { openBackupOutput, newBackupBufferedWriter = originalOutput, originalWriter }()
			if err := EncryptBackupFile(source, filepath.Join(dir, test.name), key); !errors.Is(err, errInjectedMigrationIO) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	for _, test := range []struct {
		name   string
		output migrationBackupOutput
	}{
		{name: "sync", output: migrationBackupOutput{syncErr: errInjectedMigrationIO}},
		{name: "close", output: migrationBackupOutput{closeErr: errInjectedMigrationIO}},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := openBackupOutput
			openBackupOutput = func(string) (backupOutput, error) { return &test.output, nil }
			defer func() { openBackupOutput = original }()
			if err := EncryptBackupFile(source, filepath.Join(dir, test.name), key); !errors.Is(err, errInjectedMigrationIO) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestBackupCryptoPropagatesEveryDecryptFailureStage(t *testing.T) {
	dir := t.TempDir()
	plain, encrypted := filepath.Join(dir, "plain"), filepath.Join(dir, "backup")
	key := bytes.Repeat([]byte{2}, 32)
	if err := os.WriteFile(plain, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptBackupFile(plain, encrypted, key); err != nil {
		t.Fatal(err)
	}

	t.Run("aead", func(t *testing.T) {
		original := newBackupAEAD
		newBackupAEAD = func(cipher.Block) (cipher.AEAD, error) { return nil, errInjectedMigrationIO }
		defer func() { newBackupAEAD = original }()
		if err := DecryptBackupFile(encrypted, filepath.Join(dir, "aead"), key); !errors.Is(err, errInjectedMigrationIO) {
			t.Fatalf("error=%v", err)
		}
	})

	for _, test := range []struct {
		name   string
		output migrationBackupOutput
	}{
		{name: "write", output: migrationBackupOutput{writeErr: errInjectedMigrationIO}},
		{name: "sync", output: migrationBackupOutput{syncErr: errInjectedMigrationIO}},
		{name: "close", output: migrationBackupOutput{closeErr: errInjectedMigrationIO}},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := openBackupOutput
			openBackupOutput = func(string) (backupOutput, error) { return &test.output, nil }
			defer func() { openBackupOutput = original }()
			if err := DecryptBackupFile(encrypted, filepath.Join(dir, test.name), key); !errors.Is(err, errInjectedMigrationIO) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

type migrationArtifactFile struct {
	reader  io.Reader
	info    os.FileInfo
	statErr error
}

func (file migrationArtifactFile) Read(value []byte) (int, error) { return file.reader.Read(value) }
func (file migrationArtifactFile) Stat() (os.FileInfo, error)     { return file.info, file.statErr }
func (migrationArtifactFile) Close() error                        { return nil }

func TestEvidenceIOPropagatesEveryFileFailureStage(t *testing.T) {
	originalOpen := openArtifactFile
	defer func() { openArtifactFile = originalOpen }()
	openArtifactFile = func(string) (artifactFile, error) {
		return migrationArtifactFile{reader: bytes.NewReader(nil), statErr: errInjectedMigrationIO}, nil
	}
	if _, err := FileArtifact("database", "ignored", ""); !errors.Is(err, errInjectedMigrationIO) {
		t.Fatalf("stat error=%v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	openArtifactFile = func(string) (artifactFile, error) {
		return migrationArtifactFile{reader: migrationErrorReadCloser{}, info: info}, nil
	}
	if _, err := FileArtifact("database", "ignored", ""); !errors.Is(err, errInjectedMigrationIO) {
		t.Fatalf("read error=%v", err)
	}

	evidence := validBackupEvidence(time.Now().UTC())
	for _, test := range []struct {
		name string
		set  func()
	}{
		{name: "mkdir", set: func() { makeEvidenceDir = func(string, os.FileMode) error { return errInjectedMigrationIO } }},
		{name: "write", set: func() { writeEvidenceFile = func(string, []byte, os.FileMode) error { return errInjectedMigrationIO } }},
		{name: "rename", set: func() { renameEvidenceFile = func(string, string) error { return errInjectedMigrationIO } }},
	} {
		t.Run(test.name, func(t *testing.T) {
			originalMkdir, originalWrite, originalRename := makeEvidenceDir, writeEvidenceFile, renameEvidenceFile
			test.set()
			defer func() {
				makeEvidenceDir, writeEvidenceFile, renameEvidenceFile = originalMkdir, originalWrite, originalRename
			}()
			if err := WriteJSONEvidence(filepath.Join(dir, test.name, "evidence.json"), evidence); !errors.Is(err, errInjectedMigrationIO) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
