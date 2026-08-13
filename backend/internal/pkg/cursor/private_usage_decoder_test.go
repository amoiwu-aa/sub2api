package cursor

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultPrivateUsageDecoderConfigIsProductionSafe(t *testing.T) {
	config := DefaultPrivateUsageDecoderConfig()
	require.False(t, config.Enabled)
	require.Zero(t, config.ValidatedSampleCount)

	observation := DecodePrivateUsage([]byte{0x08, 0x01}, config)
	require.Equal(t, PrivateUsageUnavailable, observation.Status)
	require.Equal(t, PrivateUsageReasonFeatureDisabled, observation.Reason)
	require.Zero(t, observation.InputTokens)
	require.Zero(t, observation.OutputTokens)
	require.Zero(t, observation.CacheReadInputTokens)
	require.Zero(t, observation.CacheCreationInputTokens)
}

func TestPrivateUsageDecoderRequiresTwentyValidatedSamples(t *testing.T) {
	observation := DecodePrivateUsage([]byte{0x08, 0x01}, PrivateUsageDecoderConfig{
		Enabled:              true,
		ValidatedSampleCount: PrivateUsageMinimumValidatedSamples - 1,
	})

	require.Equal(t, PrivateUsageUnavailable, observation.Status)
	require.Equal(t, PrivateUsageReasonInsufficientSamples, observation.Reason)
}

func TestPrivateUsageDecoderDoesNotGuessWithoutValidatedMapping(t *testing.T) {
	observation := DecodePrivateUsage([]byte{0x08, 0x01}, PrivateUsageDecoderConfig{
		Enabled:              true,
		ValidatedSampleCount: PrivateUsageMinimumValidatedSamples,
	})

	require.Equal(t, PrivateUsageUnavailable, observation.Status)
	require.Equal(t, PrivateUsageReasonNoValidatedMapping, observation.Reason)
	require.Zero(t, observation.InputTokens)
	require.Zero(t, observation.OutputTokens)
	require.Zero(t, observation.CacheReadInputTokens)
	require.Zero(t, observation.CacheCreationInputTokens)
}

func TestPrivateUsageUnknownFrameGoldenFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "private_usage_unknown.golden.json"))
	require.NoError(t, err)

	var fixture struct {
		FrameBase64 string                    `json:"frame_base64"`
		Config      PrivateUsageDecoderConfig `json:"config"`
		Want        json.RawMessage           `json:"want"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))

	frame, err := base64.StdEncoding.DecodeString(fixture.FrameBase64)
	require.NoError(t, err)
	got, err := json.Marshal(DecodePrivateUsage(frame, fixture.Config))
	require.NoError(t, err)
	require.JSONEq(t, string(fixture.Want), string(got))
}
