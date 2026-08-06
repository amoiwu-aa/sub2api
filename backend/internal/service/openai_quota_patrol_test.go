package service

import (
	"testing"
	"time"
)

func TestOpenAIQuotaExhaustedUntil(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	fiveHours := int64((5 * time.Hour).Seconds())
	sevenDays := int64((7 * 24 * time.Hour).Seconds())

	tests := []struct {
		name      string
		rateLimit *OpenAIRateLimit
		want      bool
		wantReset time.Duration
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
			want:      true,
			wantReset: 30 * time.Minute,
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
			name: "weekly-only policy pauses when seven-day window is exhausted",
			rateLimit: &OpenAIRateLimit{
				LimitReached: true,
				PrimaryWindow: &OpenAIRateLimitWindow{
					UsedPercent:        100,
					LimitWindowSeconds: sevenDays,
					ResetAfterSeconds:  3600,
				},
			},
			want:      true,
			wantReset: time.Hour,
		},
		{
			name: "multiple exhausted windows use latest reset",
			rateLimit: &OpenAIRateLimit{
				LimitReached: true,
				PrimaryWindow: &OpenAIRateLimitWindow{
					UsedPercent:        100,
					LimitWindowSeconds: sevenDays,
					ResetAfterSeconds:  7200,
				},
				SecondaryWindow: &OpenAIRateLimitWindow{
					UsedPercent:        100,
					LimitWindowSeconds: fiveHours,
					ResetAfterSeconds:  1800,
				},
			},
			want:      true,
			wantReset: 2 * time.Hour,
		},
		{
			name: "window below exhaustion threshold remains schedulable",
			rateLimit: &OpenAIRateLimit{
				LimitReached: true,
				PrimaryWindow: &OpenAIRateLimitWindow{
					UsedPercent:        99,
					LimitWindowSeconds: sevenDays,
					ResetAfterSeconds:  3600,
				},
			},
			want: false,
		},
		{
			name: "upstream must explicitly mark limit reached",
			rateLimit: &OpenAIRateLimit{
				LimitReached: false,
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
			resetAt, got := openAIQuotaExhaustedUntil(tt.rateLimit, now)
			if got != tt.want {
				t.Fatalf("openAIQuotaExhaustedUntil() = %v, want %v", got, tt.want)
			}
			if got && !resetAt.Equal(now.Add(tt.wantReset)) {
				t.Fatalf("resetAt = %s, want %s", resetAt, now.Add(tt.wantReset))
			}
		})
	}
}
