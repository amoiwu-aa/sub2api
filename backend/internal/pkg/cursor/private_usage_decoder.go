package cursor

// PrivateUsageMinimumValidatedSamples is the quality gate required before a
// private Cursor usage field mapping may be considered for production.
const PrivateUsageMinimumValidatedSamples = 20

// Keep the production gate closed until the sample and mapping gates have both
// been satisfied with real, independently validated captures.
const privateUsageDecoderProductionEnabled = false

type PrivateUsageDecodeStatus string

const (
	PrivateUsageUnavailable PrivateUsageDecodeStatus = "unavailable"
)

const (
	PrivateUsageReasonFeatureDisabled     = "feature_disabled"
	PrivateUsageReasonInsufficientSamples = "insufficient_validated_samples"
	PrivateUsageReasonNoValidatedMapping  = "no_validated_mapping"
)

type PrivateUsageDecoderConfig struct {
	Enabled              bool `json:"enabled"`
	ValidatedSampleCount int  `json:"validated_sample_count"`
}

type PrivateUsageObservation struct {
	Status                   PrivateUsageDecodeStatus `json:"status"`
	Reason                   string                   `json:"reason"`
	InputTokens              int64                    `json:"input_tokens"`
	OutputTokens             int64                    `json:"output_tokens"`
	CacheReadInputTokens     int64                    `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64                    `json:"cache_creation_input_tokens"`
}

func DefaultPrivateUsageDecoderConfig() PrivateUsageDecoderConfig {
	return PrivateUsageDecoderConfig{
		Enabled: privateUsageDecoderProductionEnabled,
	}
}

// DecodePrivateUsage intentionally does not interpret the opaque frame yet.
// No field mapping is accepted until it is backed by the required real samples.
func DecodePrivateUsage(frame []byte, config PrivateUsageDecoderConfig) PrivateUsageObservation {
	_ = frame
	switch {
	case !config.Enabled:
		return unavailablePrivateUsage(PrivateUsageReasonFeatureDisabled)
	case config.ValidatedSampleCount < PrivateUsageMinimumValidatedSamples:
		return unavailablePrivateUsage(PrivateUsageReasonInsufficientSamples)
	default:
		return unavailablePrivateUsage(PrivateUsageReasonNoValidatedMapping)
	}
}

func unavailablePrivateUsage(reason string) PrivateUsageObservation {
	return PrivateUsageObservation{
		Status: PrivateUsageUnavailable,
		Reason: reason,
	}
}
