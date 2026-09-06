package gotd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSenderReferenceSurvivesRestartWithoutPersistingIdentity(t *testing.T) {
	key := bytes.Repeat([]byte{0x61}, 32)
	want := TelegramPeerDescriptor{UserID: 777, AccessHash: 9988}
	reference := NewSenderReferences(key).Store(want)

	require.NotContains(t, reference, "777")
	peer, ok := NewSenderReferences(key).Resolve(reference)
	require.True(t, ok)
	require.Equal(t, want, peer)
}
