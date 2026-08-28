package migration

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupEnvelopeRejectsInvalidKeysFilesAndHeaders(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptBackupFile(source, filepath.Join(dir, "invalid-key"), []byte("short")); err == nil {
		t.Fatal("invalid encryption key accepted")
	}
	if err := EncryptBackupFile(filepath.Join(dir, "missing"), filepath.Join(dir, "missing-target"), bytes.Repeat([]byte{1}, 32)); err == nil {
		t.Fatal("missing encryption source accepted")
	}
	target := filepath.Join(dir, "existing")
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptBackupFile(source, target, bytes.Repeat([]byte{1}, 32)); err == nil {
		t.Fatal("existing encryption target overwritten")
	}
	if err := DecryptBackupFile(source, filepath.Join(dir, "decrypt-invalid-key"), []byte("short")); err == nil {
		t.Fatal("invalid decryption key accepted")
	}
	if err := DecryptBackupFile(filepath.Join(dir, "missing"), filepath.Join(dir, "decrypt-missing"), bytes.Repeat([]byte{1}, 32)); err == nil {
		t.Fatal("missing decryption source accepted")
	}
	if err := DecryptBackupFile(source, filepath.Join(dir, "bad-magic"), bytes.Repeat([]byte{1}, 32)); err == nil {
		t.Fatal("invalid envelope magic accepted")
	}
	wrongMagic := filepath.Join(dir, "wrong-magic")
	if err := os.WriteFile(wrongMagic, bytes.Repeat([]byte{'X'}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DecryptBackupFile(wrongMagic, filepath.Join(dir, "wrong-magic-output"), bytes.Repeat([]byte{1}, 32)); err == nil {
		t.Fatal("complete invalid envelope magic accepted")
	}

	truncatedNonce := filepath.Join(dir, "truncated-nonce")
	if err := os.WriteFile(truncatedNonce, backupMagic[:], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DecryptBackupFile(truncatedNonce, filepath.Join(dir, "nonce-output"), bytes.Repeat([]byte{1}, 32)); err == nil {
		t.Fatal("truncated nonce accepted")
	}
}

func TestBackupEnvelopeRejectsInvalidChunkFramingAndTarget(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{2}, 32)
	base := append([]byte(nil), backupMagic[:]...)
	base = append(base, bytes.Repeat([]byte{0}, 12)...)
	for _, test := range []struct {
		name    string
		length  uint32
		payload []byte
	}{
		{name: "zero", length: 0},
		{name: "oversize", length: backupChunkSize + 17},
		{name: "truncated", length: 16, payload: []byte{1}},
		{name: "authentication", length: 16, payload: bytes.Repeat([]byte{1}, 16)},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := append([]byte(nil), base...)
			raw = binary.BigEndian.AppendUint32(raw, test.length)
			raw = append(raw, test.payload...)
			source := filepath.Join(dir, test.name+".enc")
			if err := os.WriteFile(source, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, test.name+".out")
			if err := DecryptBackupFile(source, target, key); err == nil {
				t.Fatal("invalid chunk accepted")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("failed decryption retained target: %v", err)
			}
		})
	}
	truncatedLength := append([]byte(nil), base...)
	truncatedLength = append(truncatedLength, 1)
	truncatedLengthPath := filepath.Join(dir, "truncated-length.enc")
	if err := os.WriteFile(truncatedLengthPath, truncatedLength, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DecryptBackupFile(truncatedLengthPath, filepath.Join(dir, "truncated-length.out"), key); err == nil {
		t.Fatal("truncated chunk length accepted")
	}

	plain := filepath.Join(dir, "plain")
	encrypted := filepath.Join(dir, "valid.enc")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptBackupFile(plain, encrypted, key); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "existing-output")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DecryptBackupFile(encrypted, existing, key); err == nil {
		t.Fatal("existing decryption target overwritten")
	}
}

func TestBackupNonceIsIsolatedAndCarriesChunkIndex(t *testing.T) {
	base := bytes.Repeat([]byte{9}, 12)
	nonce := backupNonce(base, 0x01020304)
	if bytes.Equal(nonce, base) || !bytes.Equal(nonce[len(nonce)-4:], []byte{1, 2, 3, 4}) {
		t.Fatalf("nonce=%v base=%v", nonce, base)
	}
	if !bytes.Equal(base, bytes.Repeat([]byte{9}, 12)) {
		t.Fatalf("base nonce mutated: %v", base)
	}
}
