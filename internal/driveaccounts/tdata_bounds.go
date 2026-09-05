package driveaccounts

import (
	"bytes"
	"crypto/aes"
	"crypto/md5"  // Telegram Desktop file integrity and filenames use MD5.
	"crypto/sha1" // Telegram Desktop local encryption protocol uses SHA-1.
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/gotd/ige"
	"github.com/gotd/td/bin"
	tdcrypto "github.com/gotd/td/crypto"
	"golang.org/x/crypto/pbkdf2"
)

// validateTDataBounds checks attacker-controlled allocation counts before invoking
// gotd's decoder. Its decrypted contents never leave this in-memory admission step.
func validateTDataBounds(root fs.FS) error {
	keyData, err := tdFile(root, "key_data")
	if err != nil {
		return err
	}
	defer clear(keyData)
	salt, remaining, err := tdArray(keyData)
	if err != nil || len(salt) != 32 {
		return ErrUnsafe
	}
	encrypted, remaining, err := tdArray(remaining)
	if err != nil {
		return err
	}
	h := sha512.New()
	h.Write(salt)
	h.Write(salt)
	var passkey tdcrypto.Key
	copy(passkey[:], pbkdf2.Key(h.Sum(nil), salt, 1, 256, sha512.New))
	defer clear(passkey[:])
	inner, err := tdDecrypt(encrypted, passkey)
	if err != nil {
		return err
	}
	defer clear(inner)
	if len(inner) < 260 || binary.LittleEndian.Uint32(inner) < 256 || uint64(binary.LittleEndian.Uint32(inner)) > uint64(len(inner)-4) {
		return ErrUnsafe
	}
	var local tdcrypto.Key
	copy(local[:], inner[4:260])
	defer clear(local[:])
	encrypted, _, err = tdArray(remaining)
	if err != nil {
		return err
	}
	info, err := tdDecrypt(encrypted, local)
	if err != nil {
		return err
	}
	defer clear(info)
	if len(info) < 8 {
		return ErrUnsafe
	}
	count := binary.BigEndian.Uint32(info[4:8])
	if count == 0 || count > 1000 || uint64(count)*4 > uint64(len(info)-8) {
		return ErrUnsafe
	}
	seen := map[uint32]bool{}
	for n := uint32(0); n < count; n++ {
		index := binary.BigEndian.Uint32(info[8+n*4:])
		if seen[index] || index > 1000 {
			return ErrUnsafe
		}
		seen[index] = true
		label := "data"
		if index > 0 {
			label = fmt.Sprintf("data#%d", index+1)
		}
		md := md5.Sum([]byte(label))
		encoded := []byte(strings.ToUpper(hex.EncodeToString(md[:8])))
		for p := 0; p < len(encoded); p += 2 {
			encoded[p], encoded[p+1] = encoded[p+1], encoded[p]
		}
		content, e := tdFile(root, string(encoded))
		if e != nil {
			return e
		}
		data, _, e := tdArray(content)
		if e != nil {
			clear(content)
			return e
		}
		decoded, e := tdDecrypt(data, local)
		clear(content)
		if e != nil {
			return e
		}
		e = validateAuthorizationBounds(decoded)
		clear(decoded)
		if e != nil {
			return e
		}
	}
	return nil
}
func validateAuthorizationBounds(data []byte) error {
	if len(data) < 24 || binary.BigEndian.Uint32(data[4:8]) != 0x4b {
		return ErrUnsafe
	}
	offset := 20
	if binary.BigEndian.Uint64(data[12:20]) == 0xffffffffffffffff {
		offset += 12
	}
	if len(data) < offset+4 {
		return ErrUnsafe
	}
	keys := binary.BigEndian.Uint32(data[offset : offset+4])
	if keys == 0 || keys > 16 || uint64(keys)*260 > uint64(len(data)-offset-4) {
		return ErrUnsafe
	}
	return nil
}
func tdFile(root fs.FS, base string) ([]byte, error) {
	for _, suffix := range []string{"s", "0", "1"} {
		f, err := root.Open(base + suffix)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, ErrUnsafe
		}
		raw, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
		f.Close()
		if err != nil || len(raw) > 16<<20 || len(raw) < 24 || string(raw[:4]) != "TDF$" {
			clear(raw)
			return nil, ErrUnsafe
		}
		body := raw[8 : len(raw)-16]
		h := md5.New()
		h.Write(body)
		var size [4]byte
		binary.LittleEndian.PutUint32(size[:], uint32(len(body)))
		h.Write(size[:])
		h.Write(raw[4:8])
		h.Write(raw[:4])
		if !bytes.Equal(h.Sum(nil), raw[len(raw)-16:]) {
			clear(raw)
			return nil, ErrUnsafe
		}
		return body, nil
	}
	return nil, ErrUnsafe
}
func tdArray(data []byte) ([]byte, []byte, error) {
	if len(data) < 4 {
		return nil, nil, ErrUnsafe
	}
	size := uint64(binary.BigEndian.Uint32(data))
	if size > uint64(len(data)-4) {
		return nil, nil, ErrUnsafe
	}
	return data[4 : 4+size], data[4+size:], nil
}
func tdDecrypt(encrypted []byte, key tdcrypto.Key) ([]byte, error) {
	if len(encrypted) < 32 || len(encrypted)%16 != 0 {
		return nil, ErrUnsafe
	}
	var msg bin.Int128
	copy(msg[:], encrypted[:16])
	k, iv := tdcrypto.OldKeys(key, msg, tdcrypto.Server)
	defer clear(k[:])
	defer clear(iv[:])
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, ErrUnsafe
	}
	plain := make([]byte, len(encrypted)-16)
	ige.DecryptBlocks(block, iv[:], plain, encrypted[16:])
	sum := sha1.Sum(plain)
	if !bytes.Equal(sum[:16], msg[:]) {
		clear(plain)
		return nil, ErrUnsafe
	}
	return plain, nil
}
