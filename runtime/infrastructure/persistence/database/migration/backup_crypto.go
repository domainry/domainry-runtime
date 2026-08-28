package migration

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

var backupMagic = [8]byte{'V', 'R', 'B', 'K', '0', '0', '0', '1'}

const backupChunkSize = 4 << 20

type backupOutput interface {
	io.Writer
	Sync() error
	Close() error
}

type backupBufferedWriter interface {
	io.Writer
	Flush() error
}

var (
	newBackupAEAD    = cipher.NewGCM
	openBackupInput  = func(path string) (io.ReadCloser, error) { return os.Open(path) }
	openBackupOutput = func(path string) (backupOutput, error) {
		return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	newBackupBufferedWriter = func(writer io.Writer) backupBufferedWriter {
		return bufio.NewWriterSize(writer, 256<<10)
	}
	readBackupNonce  = rand.Read
	removeBackupFile = os.Remove
)

func EncryptBackupFile(source, target string, key []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("initialize backup encryption: %w", err)
	}
	aead, err := newBackupAEAD(block)
	if err != nil {
		return err
	}
	input, err := openBackupInput(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := openBackupOutput(target)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = removeBackupFile(target)
		}
	}()
	writer := newBackupBufferedWriter(output)
	if _, err := writer.Write(backupMagic[:]); err != nil {
		return err
	}
	baseNonce := make([]byte, aead.NonceSize())
	if _, err := readBackupNonce(baseNonce); err != nil {
		return err
	}
	if _, err := writer.Write(baseNonce); err != nil {
		return err
	}
	plain := make([]byte, backupChunkSize)
	for index := uint32(0); ; index++ {
		read, readErr := io.ReadFull(input, plain)
		if readErr == io.EOF {
			break
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		nonce := backupNonce(baseNonce, index)
		sealed := aead.Seal(nil, nonce, plain[:read], backupMagic[:])
		if err := binary.Write(writer, binary.BigEndian, uint32(len(sealed))); err != nil {
			return err
		}
		if _, err := writer.Write(sealed); err != nil {
			return err
		}
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	ok = true
	return output.Close()
}

func DecryptBackupFile(source, target string, key []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("initialize backup decryption: %w", err)
	}
	aead, err := newBackupAEAD(block)
	if err != nil {
		return err
	}
	input, err := openBackupInput(source)
	if err != nil {
		return err
	}
	defer input.Close()
	reader := bufio.NewReaderSize(input, 256<<10)
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || magic != backupMagic {
		return fmt.Errorf("backup.envelope_invalid")
	}
	baseNonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(reader, baseNonce); err != nil {
		return err
	}
	output, err := openBackupOutput(target)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = removeBackupFile(target)
		}
	}()
	for index := uint32(0); ; index++ {
		var length uint32
		err := binary.Read(reader, binary.BigEndian, &length)
		if err == io.EOF {
			break
		}
		if err != nil || length == 0 || length > backupChunkSize+uint32(aead.Overhead()) {
			return fmt.Errorf("backup.chunk_invalid")
		}
		sealed := make([]byte, length)
		if _, err := io.ReadFull(reader, sealed); err != nil {
			return err
		}
		plain, err := aead.Open(nil, backupNonce(baseNonce, index), sealed, backupMagic[:])
		if err != nil {
			return fmt.Errorf("backup.authentication_failed: %w", err)
		}
		if _, err := output.Write(plain); err != nil {
			return err
		}
	}
	if err := output.Sync(); err != nil {
		return err
	}
	ok = true
	return output.Close()
}

func backupNonce(base []byte, index uint32) []byte {
	nonce := append([]byte(nil), base...)
	binary.BigEndian.PutUint32(nonce[len(nonce)-4:], index)
	return nonce
}
