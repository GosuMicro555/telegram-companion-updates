package wails

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"
)

func TestGetKeywordSettingsMarshalsFreshKeywordsAsArray(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings()}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)

	settings, err := bindings.GetKeywordSettings()
	require.NoError(t, err)

	payload, err := json.Marshal(settings)
	require.NoError(t, err)
	require.JSONEq(t, `{"keywords":[],"sharedReply":"","privateReply":"","deliveryMode":"comments","minusKeywords":[],"directMessageKeywords":[],"revision":1}`, string(payload))
}
