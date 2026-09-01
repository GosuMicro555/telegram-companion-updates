package bootstrapstate

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/scrypt"
)

const (
	bundleMagicV1  = "TCSEED1\n"
	bundleMagicV2  = "TCSEED2\n"
	bundleChunk    = 1024 * 1024
	bundleSaltSize = 16
	headerSize     = len(bundleMagicV1) + bundleSaltSize + chacha20poly1305.NonceSizeX + 4
)

type encryptedWriter struct {
	destination io.Writer
	aead        cipherAEAD
	nonce       [chacha20poly1305.NonceSizeX]byte
	buffer      []byte
	index       uint64
	closed      bool
}

type cipherAEAD interface {
	NonceSize() int
	Overhead() int
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

func newEncryptedWriter(destination io.Writer, license string) (*encryptedWriter, error) {
	if destination == nil || strings.TrimSpace(license) == "" {
		return nil, errors.New("bootstrap state: destination and license are required")
	}
	return newEncryptedWriterWithKeySource(destination, bundleMagicV1, func(salt []byte) ([]byte, error) {
		return deriveBundleKey(license, salt)
	})
}

func newEncryptedWriterWithKey(destination io.Writer, key []byte) (*encryptedWriter, error) {
	if destination == nil || len(key) != chacha20poly1305.KeySize {
		return nil, errors.New("bootstrap state: destination and 32-byte key are required")
	}
	keyCopy := append([]byte(nil), key...)
	return newEncryptedWriterWithKeySource(destination, bundleMagicV2, func([]byte) ([]byte, error) {
		return append([]byte(nil), keyCopy...), nil
	})
}

func newEncryptedWriterWithKeySource(destination io.Writer, magic string, keySource func([]byte) ([]byte, error)) (*encryptedWriter, error) {
	salt := make([]byte, bundleSaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	key, err := keySource(salt)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	var nonce [chacha20poly1305.NonceSizeX]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:16]); err != nil {
		return nil, err
	}
	header := make([]byte, 0, headerSize)
	header = append(header, magic...)
	header = append(header, salt...)
	header = append(header, nonce[:]...)
	var chunk [4]byte
	binary.BigEndian.PutUint32(chunk[:], bundleChunk)
	header = append(header, chunk[:]...)
	if _, err := destination.Write(header); err != nil {
		return nil, err
	}
	return &encryptedWriter{destination: destination, aead: aead, nonce: nonce, buffer: make([]byte, 0, bundleChunk)}, nil
}

func (writer *encryptedWriter) Write(data []byte) (int, error) {
	if writer == nil || writer.closed {
		return 0, errors.New("bootstrap state: encrypted writer is closed")
	}
	written := 0
	for len(data) > 0 {
		space := bundleChunk - len(writer.buffer)
		amount := len(data)
		if amount > space {
			amount = space
		}
		writer.buffer = append(writer.buffer, data[:amount]...)
		data = data[amount:]
		written += amount
		if len(writer.buffer) == bundleChunk {
			if err := writer.writeRecord(0, writer.buffer); err != nil {
				return written, err
			}
			writer.buffer = writer.buffer[:0]
		}
	}
	return written, nil
}

func (writer *encryptedWriter) Close() error {
	if writer == nil || writer.closed {
		return nil
	}
	writer.closed = true
	if len(writer.buffer) > 0 {
		if err := writer.writeRecord(0, writer.buffer); err != nil {
			return err
		}
	}
	return writer.writeRecord(1, nil)
}

func (writer *encryptedWriter) writeRecord(flag byte, plaintext []byte) error {
	nonce := writer.recordNonce()
	aad := recordAAD(flag, writer.index)
	ciphertext := writer.aead.Seal(nil, nonce, plaintext, aad)
	header := make([]byte, 5)
	header[0] = flag
	binary.BigEndian.PutUint32(header[1:], uint32(len(ciphertext)))
	if _, err := writer.destination.Write(header); err != nil {
		return err
	}
	if _, err := writer.destination.Write(ciphertext); err != nil {
		return err
	}
	writer.index++
	return nil
}

func (writer *encryptedWriter) recordNonce() []byte {
	nonce := writer.nonce
	binary.BigEndian.PutUint64(nonce[16:], writer.index)
	return nonce[:]
}

type encryptedReader struct {
	source io.Reader
	aead   cipherAEAD
	nonce  [chacha20poly1305.NonceSizeX]byte
	chunk  uint32
	index  uint64
	buffer []byte
	done   bool
}

func newEncryptedReader(source io.Reader, license string) (*encryptedReader, error) {
	if source == nil || strings.TrimSpace(license) == "" {
		return nil, ErrInvalidBundle
	}
	return newEncryptedReaderWithKeySource(source, bundleMagicV1, func(salt []byte) ([]byte, error) {
		return deriveBundleKey(license, salt)
	})
}

func newEncryptedReaderWithKey(source io.Reader, key []byte) (*encryptedReader, error) {
	if source == nil || len(key) != chacha20poly1305.KeySize {
		return nil, ErrInvalidBundle
	}
	keyCopy := append([]byte(nil), key...)
	return newEncryptedReaderWithKeySource(source, bundleMagicV2, func([]byte) ([]byte, error) {
		return append([]byte(nil), keyCopy...), nil
	})
}

func newEncryptedReaderWithKeySource(source io.Reader, magic string, keySource func([]byte) ([]byte, error)) (*encryptedReader, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(source, header); err != nil {
		return nil, ErrInvalidBundle
	}
	if string(header[:len(magic)]) != magic {
		return nil, ErrInvalidBundle
	}
	offset := len(magic)
	salt := header[offset : offset+bundleSaltSize]
	offset += bundleSaltSize
	var nonce [chacha20poly1305.NonceSizeX]byte
	copy(nonce[:], header[offset:offset+chacha20poly1305.NonceSizeX])
	offset += chacha20poly1305.NonceSizeX
	chunk := binary.BigEndian.Uint32(header[offset:])
	if chunk == 0 || chunk > 16*bundleChunk {
		return nil, ErrInvalidBundle
	}
	key, err := keySource(salt)
	if err != nil {
		return nil, ErrInvalidBundle
	}
	defer clear(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, ErrInvalidBundle
	}
	return &encryptedReader{source: source, aead: aead, nonce: nonce, chunk: chunk}, nil
}

func (reader *encryptedReader) Read(destination []byte) (int, error) {
	for len(reader.buffer) == 0 && !reader.done {
		if err := reader.readRecord(); err != nil {
			return 0, err
		}
	}
	if len(reader.buffer) == 0 && reader.done {
		return 0, io.EOF
	}
	count := copy(destination, reader.buffer)
	reader.buffer = reader.buffer[count:]
	return count, nil
}

func (reader *encryptedReader) readRecord() error {
	header := make([]byte, 5)
	if _, err := io.ReadFull(reader.source, header); err != nil {
		return ErrInvalidBundle
	}
	flag := header[0]
	length := binary.BigEndian.Uint32(header[1:])
	if flag > 1 || length < uint32(reader.aead.Overhead()) || length > reader.chunk+uint32(reader.aead.Overhead()) {
		return ErrInvalidBundle
	}
	ciphertext := make([]byte, length)
	if _, err := io.ReadFull(reader.source, ciphertext); err != nil {
		return ErrInvalidBundle
	}
	nonce := reader.nonce
	binary.BigEndian.PutUint64(nonce[16:], reader.index)
	plaintext, err := reader.aead.Open(nil, nonce[:], ciphertext, recordAAD(flag, reader.index))
	if err != nil {
		return ErrInvalidBundle
	}
	reader.index++
	if flag == 1 {
		if len(plaintext) != 0 {
			return ErrInvalidBundle
		}
		var extra [1]byte
		if _, err := reader.source.Read(extra[:]); !errors.Is(err, io.EOF) {
			return ErrInvalidBundle
		}
		reader.done = true
		return nil
	}
	if len(plaintext) == 0 {
		return ErrInvalidBundle
	}
	reader.buffer = plaintext
	return nil
}

func deriveBundleKey(license string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(strings.TrimSpace(license)), salt, 32768, 8, 1, chacha20poly1305.KeySize)
}

func recordAAD(flag byte, index uint64) []byte {
	aad := make([]byte, 9)
	aad[0] = flag
	binary.BigEndian.PutUint64(aad[1:], index)
	return aad
}
