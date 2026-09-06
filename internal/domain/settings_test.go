package domain_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

func TestAppSettingsJoinIntervalRangeUsesLegacyDefaults(t *testing.T) {
	interval, err := (domain.AppSettings{}).JoinIntervalRange()

	require.NoError(t, err)
	require.Equal(t, 10, interval.MinMinutes)
	require.Equal(t, 60, interval.MaxMinutes)
}

func TestNewJoinIntervalRangeAcceptsConfiguredRange(t *testing.T) {
	interval, err := domain.NewJoinIntervalRange(20, 45)

	require.NoError(t, err)
	require.Equal(t, 20, interval.MinMinutes)
	require.Equal(t, 45, interval.MaxMinutes)
}

func TestNewJoinIntervalRangeAcceptsFullConfiguredBounds(t *testing.T) {
	interval, err := domain.NewJoinIntervalRange(0, 3000)

	require.NoError(t, err)
	require.Equal(t, 0, interval.MinMinutes)
	require.Equal(t, 3000, interval.MaxMinutes)
}

func TestNewJoinIntervalRangeRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		min     int
		max     int
		message string
	}{
		{name: "negative minimum", min: -1, max: 60, message: "minimum must be between 0 and 3000 minutes"},
		{name: "negative maximum", min: 10, max: -1, message: "maximum must be between 0 and 3000 minutes"},
		{name: "maximum above range", min: 10, max: 3001, message: "maximum must be between 0 and 3000 minutes"},
		{name: "minimum above maximum", min: 40, max: 20, message: "minimum must not exceed maximum"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := domain.NewJoinIntervalRange(test.min, test.max)

			require.ErrorIs(t, err, domain.ErrInvalidJoinInterval)
			require.Contains(t, err.Error(), test.message)
		})
	}
}

func TestAppSettingsJSONRejectsInvalidJoinIntervalRange(t *testing.T) {
	var settings domain.AppSettings

	err := json.Unmarshal([]byte(`{"joinIntervalMinMinutes":-1,"joinIntervalMaxMinutes":60}`), &settings)

	require.ErrorIs(t, err, domain.ErrInvalidJoinInterval)
}

func TestAppSettingsJoinIntervalEnabledDefaultsForLegacyJSONAndPreservesExplicitDisable(t *testing.T) {
	var legacy domain.AppSettings
	require.NoError(t, json.Unmarshal([]byte(`{"joinIntervalMinMinutes":10,"joinIntervalMaxMinutes":60}`), &legacy))
	require.True(t, legacy.JoinIntervalEnabled)

	var disabled domain.AppSettings
	require.NoError(t, json.Unmarshal([]byte(`{"joinIntervalEnabled":false,"joinIntervalMinMinutes":0,"joinIntervalMaxMinutes":0,"groupRestHours":36,"groupRestEnabled":false}`), &disabled))
	require.False(t, disabled.JoinIntervalEnabled)
	require.Equal(t, 0, disabled.JoinIntervalMinMinutes)
	require.Equal(t, 0, disabled.JoinIntervalMaxMinutes)
}

func TestAppSettingsJSONPreservesExplicitZeroJoinInterval(t *testing.T) {
	var settings domain.AppSettings

	require.NoError(t, json.Unmarshal([]byte(`{"joinIntervalEnabled":true,"joinIntervalMinMinutes":0,"joinIntervalMaxMinutes":0}`), &settings))
	interval, err := settings.JoinIntervalRange()

	require.NoError(t, err)
	require.Equal(t, 0, interval.MinMinutes)
	require.Equal(t, 0, interval.MaxMinutes)
}

func TestAppSettingsNormalizedPreservesExplicitDisabledZeroJoinInterval(t *testing.T) {
	settings := domain.AppSettings{
		RepliesPerMinute:       19,
		MinIntervalSeconds:     2,
		JoinIntervalEnabled:    false,
		JoinIntervalMinMinutes: 0,
		JoinIntervalMaxMinutes: 0,
		GroupRestHours:         36,
	}

	normalized := settings.Normalized()

	require.Zero(t, normalized.JoinIntervalMinMinutes)
	require.Zero(t, normalized.JoinIntervalMaxMinutes)
}

func TestAppSettingsGroupRestHoursUsesLegacyDefault(t *testing.T) {
	settings := (domain.AppSettings{}).Normalized()

	require.Equal(t, 36, settings.GroupRestHours)
}

func TestAppSettingsGroupRestEnabledDefaultsForLegacyJSONAndPreservesExplicitDisable(t *testing.T) {
	var legacy domain.AppSettings
	require.NoError(t, json.Unmarshal([]byte(`{"groupRestHours":36}`), &legacy))
	require.True(t, legacy.GroupRestEnabled)

	var disabled domain.AppSettings
	require.NoError(t, json.Unmarshal([]byte(`{"groupRestHours":36,"groupRestEnabled":false}`), &disabled))
	require.False(t, disabled.GroupRestEnabled)
}

func TestAppSettingsJSONRejectsInvalidGroupRestHours(t *testing.T) {
	tests := []struct {
		name  string
		hours int
	}{
		{name: "below range", hours: -1},
		{name: "above range", hours: 721},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var settings domain.AppSettings

			err := json.Unmarshal(
				[]byte(`{"groupRestHours":`+fmt.Sprint(test.hours)+`}`),
				&settings,
			)

			require.ErrorIs(t, err, domain.ErrInvalidGroupRestHours)
		})
	}
}

func TestKeywordSettingsDeliveryModeDefaultsToCommentsAndPreservesExplicitMode(t *testing.T) {
	var legacy domain.KeywordSettings
	require.NoError(t, json.Unmarshal([]byte(`{"sharedReply":"comment"}`), &legacy))
	require.Equal(t, domain.KeywordDeliveryModeComments, legacy.DeliveryMode)

	var both domain.KeywordSettings
	require.NoError(t, json.Unmarshal([]byte(`{"deliveryMode":"both"}`), &both))
	require.Equal(t, domain.KeywordDeliveryModeBoth, both.DeliveryMode)
}
