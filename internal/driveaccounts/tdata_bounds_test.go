package driveaccounts

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/ige"
	"github.com/gotd/td/bin"
	tdcrypto "github.com/gotd/td/crypto"
	"golang.org/x/crypto/pbkdf2"
	appcrypto "telegram-companion/internal/service/crypto"
)

// Completely synthetic encrypted Telegram Desktop data; no live sessions.
func syntheticTData(t *testing.T, accountCount, keyCount uint32) map[string][]byte {
	t.Helper()
	var local, auth tdcrypto.Key
	for n := range local {
		local[n] = byte(n)
		auth[n] = byte(255 - n)
	}
	salt := bytes.Repeat([]byte{17}, 32)
	h := sha512.New()
	h.Write(salt)
	h.Write(salt)
	var passkey tdcrypto.Key
	copy(passkey[:], pbkdf2.Key(h.Sum(nil), salt, 1, 256, sha512.New))
	encrypt := func(data []byte, key tdcrypto.Key) []byte {
		for len(data)%16 != 0 {
			data = append(data, 0)
		}
		sum := sha1.Sum(data)
		var msg bin.Int128
		copy(msg[:], sum[:16])
		k, iv := tdcrypto.OldKeys(key, msg, tdcrypto.Server)
		block, e := aes.NewCipher(k[:])
		if e != nil {
			t.Fatal(e)
		}
		result := make([]byte, 16+len(data))
		copy(result, msg[:])
		ige.EncryptBlocks(block, iv[:], result[16:], data)
		return result
	}
	array := func(data []byte) []byte {
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, uint32(len(data)))
		return append(out, data...)
	}
	wrap := func(data []byte) []byte {
		version := []byte{1, 0, 0, 0}
		length := make([]byte, 4)
		binary.LittleEndian.PutUint32(length, uint32(len(data)))
		h := md5.New()
		h.Write(data)
		h.Write(length)
		h.Write(version)
		h.Write([]byte("TDF$"))
		out := append([]byte("TDF$"), version...)
		out = append(out, data...)
		return append(out, h.Sum(nil)...)
	}
	inner := make([]byte, 272)
	binary.LittleEndian.PutUint32(inner, 268)
	copy(inner[4:], local[:])
	info := make([]byte, 16)
	binary.LittleEndian.PutUint32(info, 16)
	binary.BigEndian.PutUint32(info[4:], accountCount)
	body := append(array(salt), array(encrypt(inner, passkey))...)
	body = append(body, array(encrypt(info, local))...)
	mtp := make([]byte, 28)
	binary.BigEndian.PutUint32(mtp, 280)
	binary.BigEndian.PutUint32(mtp[4:], 0x4b)
	binary.BigEndian.PutUint32(mtp[12:], 12345)
	binary.BigEndian.PutUint32(mtp[16:], 2)
	binary.BigEndian.PutUint32(mtp[20:], keyCount)
	binary.BigEndian.PutUint32(mtp[24:], 2)
	mtp = append(mtp, auth[:]...)
	return map[string][]byte{"key_datas": wrap(body), "D877F783D5D3EF8Cs": wrap(array(encrypt(mtp, local)))}
}
func writeSyntheticTData(t *testing.T, files map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestReadRealSyntheticTDataFormat(t *testing.T) {
	root := writeSyntheticTData(t, syntheticTData(t, 1, 1))
	result, err := readCandidates(context.Background(), root, appcrypto.AppCredentials{AppID: 123, AppHash: "synthetic"})
	if err != nil || len(result) != 1 || result[0].UserID != 12345 {
		t.Fatal("synthetic encrypted TData could not be read", err)
	}
}
func TestTDataAdmissionRejectsUntrustedAllocationCounts(t *testing.T) {
	for _, counts := range [][2]uint32{{0xffffffff, 1}, {1, 0xffffffff}, {1, 99}, {1001, 1}} {
		root := writeSyntheticTData(t, syntheticTData(t, counts[0], counts[1]))
		opened, err := os.OpenRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		err = validateTDataBounds(opened.FS())
		opened.Close()
		if err == nil {
			t.Fatalf("accepted unsafe allocation counts %v", counts)
		}
	}
}
