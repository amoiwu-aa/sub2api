package service

import (
	"testing"
	"time"
)

func TestOpenAIQuotaFiveHourExhaustedUntil(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	fiveHours := int64((5 * time.Hour).Seconds())
	sevenDays := int64((7 * 24 * time.Hour).Seconds())

	tests := []struct {
		name      string
		rateLimit *OpenAIRateLimit
		want      bool
	}{
		{
			name: "explicit exhausted five-hour window",
			rateLimit: &OpenAIRateLimit{
				LimitReached: true,
				SecondaryWindow: &OpenAIRateLimitWindow{
					UsedPercent:        100,
					LimitWindowSeconds: fiveHours,
					ResetAfterSeconds:  1800,
				},
			},
			want: true,
		},
		{
			name: "zero-length window is unavailable not exhausted",
			rateLimit: &OpenAIRateLimit{
				LimitReached: true,
				SecondaryWindow: &OpenAIRateLimitWindow{
					UsedPercent:        0,
					LimitWindowSeconds: 0,
					ResetAfterSeconds:  0,
				},
			},
			want: false,
		},
		{
			name: "seven-day exhaustion does not mark five-hour rate limit",
			rateLimit: &OpenAIRateLimit{
				LimitReached: true,
				PrimaryWindow: &OpenAIRateLimitWindow{
					UsedPercent:        100,
					LimitWindowSeconds: sevenDays,
					ResetAfterSeconds:  3600,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetAt, got := openAIQuotaFiveHourExhaustedUntil(tt.rateLimit, now)
			if got != tt.want {
				t.Fatalf("openAIQuotaFiveHourExhaustedUntil() = %v, want %v", got, tt.want)
			}
			if got && !resetAt.Equal(now.Add(30*time.Minute)) {
				t.Fatalf("resetAt = %s, want %s", resetAt, now.Add(30*time.Minute))
			}
		})
	}
}
