package analytics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultMoneyShortageProfilePrefersNeedPhrases(t *testing.T) {
	profile := DefaultMoneyShortageProfile()
	require.NotEmpty(t, profile.ID)
	require.Greater(t, profile.Score([]string{"не", "хватает", "денег"}), profile.Score([]string{"продам", "старый", "стол"}))
}

func TestProfileExclusionsSuppressCandidate(t *testing.T) {
	profile := Profile{PositiveExamples: []string{"нет денег"}, Exclusions: []string{"не проблема"}}
	require.Zero(t, profile.Score([]string{"нет", "денег", "не", "проблема"}))
}
