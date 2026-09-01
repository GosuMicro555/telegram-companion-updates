package gotd

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

type SenderReferences struct {
	aead cipher.AEAD
}

type TelegramPeerDescriptor struct {
	UserID     int64  `json:"user_id"`
	AccessHash int64  `json:"access_hash"`
	Username   string `json:"username,omitempty"`
}

func NewSenderReferences(keys ...[]byte) *SenderReferences {
	key := make([]byte, chacha20poly1305.KeySize)
	if len(keys) == 1 && len(keys[0]) == chacha20poly1305.KeySize {
		copy(key, keys[0])
	} else {
		_, _ = rand.Read(key)
	}
	aead, _ := chacha20poly1305.NewX(key)
	clear(key)
	return &SenderReferences{aead: aead}
}

func (r *SenderReferences) Store(peer TelegramPeerDescriptor) string {
	peer.Username = strings.TrimSpace(strings.TrimPrefix(peer.Username, "@"))
	if r == nil || peer.UserID == 0 || peer.AccessHash == 0 {
		return ""
	}
	plaintext, err := json.Marshal(peer)
	if err != nil {
		return ""
	}
	nonce := make([]byte, r.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	sealed := r.aead.Seal(nonce, nonce, plaintext, []byte("telegram-dm-target-v2"))
	return base64.RawURLEncoding.EncodeToString(sealed)
}

func (r *SenderReferences) Resolve(reference string) (TelegramPeerDescriptor, bool) {
	if r == nil {
		return TelegramPeerDescriptor{}, false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(reference)
	if err != nil || len(sealed) < r.aead.NonceSize()+r.aead.Overhead() {
		return TelegramPeerDescriptor{}, false
	}
	nonce := sealed[:r.aead.NonceSize()]
	plaintext, err := r.aead.Open(nil, nonce, sealed[r.aead.NonceSize():], []byte("telegram-dm-target-v2"))
	if err != nil {
		return TelegramPeerDescriptor{}, false
	}
	var peer TelegramPeerDescriptor
	if err := json.Unmarshal(plaintext, &peer); err != nil || peer.UserID == 0 || peer.AccessHash == 0 {
		return TelegramPeerDescriptor{}, false
	}
	return peer, true
}
