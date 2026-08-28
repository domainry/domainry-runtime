package integrationcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
)

func DescriptorContractHash() (string, error) {
	return descriptorContractHashFromFS(descriptors)
}

func descriptorContractHashFromFS(source fs.FS) (string, error) {
	paths, _ := fs.Glob(source, "*/connector.json")
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		raw, readErr := fs.ReadFile(source, path)
		if readErr != nil {
			return "", fmt.Errorf("read runtime connector descriptor %s: %w", path, readErr)
		}
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
