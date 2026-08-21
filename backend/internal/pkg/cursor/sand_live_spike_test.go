//go:build livespike

package cursor

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestLiveSandClaudeModel(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("CURSOR_ACCESS_TOKEN"))
	if token == "" {
		t.Skip("CURSOR_ACCESS_TOKEN is not set")
	}
	modelID := strings.TrimSpace(os.Getenv("SAND_SPIKE_MODEL"))
	if modelID == "" {
		modelID = "claude-opus-4-8"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	telemetry := DeriveTelemetryIDs(token)
	result, err := RunAgentTurn(ctx, &AgentOptions{
		HTTPClient: &http.Client{
			Timeout:   3 * time.Minute,
			Transport: &http.Transport{ForceAttemptHTTP2: true},
		},
		AccessToken:   token,
		Telemetry:     telemetry,
		Profile:       AgentProfileSand,
		MachineID:     strings.TrimSpace(os.Getenv("SAND_MACHINE_ID")),
		ClientVersion: SandClientVersion,
		SandNamespace: "prod",
	}, AgentTurnInput{
		Text:           "Reply with the single lowercase word: ok. Do not use tools.",
		ConversationID: uuid.NewString(),
		ModelID:        modelID,
		ModelParams:    DefaultModelParams(),
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, strings.TrimSpace(result.Text))
	t.Logf("model=%s turn_ended=%v text=%q", modelID, result.TurnEnded, result.Text)
}

func TestLiveSandQuotaSnapshot(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("CURSOR_ACCESS_TOKEN"))
	if token == "" {
		t.Skip("CURSOR_ACCESS_TOKEN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	opts := &Options{HTTPClient: &http.Client{Timeout: time.Minute}}
	telemetry := DeriveTelemetryIDs(token)
	status, err := FetchSandUsageStatus(
		ctx,
		opts,
		token,
		telemetry.MachineID,
		SandClientVersion,
		"prod",
	)
	require.NoError(t, err)
	require.NotNil(t, status)

	period, err := FetchSandCurrentPeriodUsage(
		ctx,
		opts,
		token,
		telemetry.MachineID,
		SandClientVersion,
		"prod",
	)
	require.NoError(t, err)
	require.NotNil(t, period)

	weeklyPercent := 0.0
	if status.UsagePercent != nil {
		weeklyPercent = *status.UsagePercent
	}
	t.Logf(
		"weekly_percent=%.9f available=%v next_reset=%s",
		weeklyPercent,
		status.HasAvailableUsage,
		status.NextReset.Format(time.RFC3339Nano),
	)
	if period.PlanUsage != nil {
		autoPercent := 0.0
		if period.PlanUsage.AutoPercentUsed != nil {
			autoPercent = *period.PlanUsage.AutoPercentUsed
		}
		apiPercent := 0.0
		if period.PlanUsage.APIPercentUsed != nil {
			apiPercent = *period.PlanUsage.APIPercentUsed
		}
		totalPercent := 0.0
		if period.PlanUsage.TotalPercentUsed != nil {
			totalPercent = *period.PlanUsage.TotalPercentUsed
		}
		t.Logf(
			"auto_percent=%.9f api_percent=%.9f total_percent=%.9f total_cents=%.0f included_cents=%.0f bonus_cents=%.0f",
			autoPercent,
			apiPercent,
			totalPercent,
			period.PlanUsage.TotalSpendCents,
			period.PlanUsage.IncludedSpendCents,
			period.PlanUsage.BonusSpendCents,
		)
	}
	t.Logf("auto_bucket_models=%q", period.AutoBucketModels)

	models, err := FetchSandUsableModels(
		ctx,
		opts,
		token,
		telemetry.MachineID,
		SandClientVersion,
		"prod",
		nil,
	)
	require.NoError(t, err)
	t.Logf("usable_model_count=%d", len(models))

	displayIDs := make(map[string]struct{})
	for _, model := range models {
		displayID := strings.TrimSpace(model.DisplayModelID)
		if displayID == "" {
			displayID = model.ModelID
		}
		displayIDs[displayID] = struct{}{}
	}
	collapsed := make([]string, 0, len(displayIDs))
	for displayID := range displayIDs {
		collapsed = append(collapsed, displayID)
	}
	sort.Strings(collapsed)
	t.Logf("display_model_ids=%q", collapsed)

	for _, model := range models {
		if matchSandSupportedModelID(model.DisplayModelID) != "" ||
			matchSandSupportedModelID(model.ModelID) != "" {
			t.Logf(
				"model_detail id=%q display_id=%q name=%q short=%q aliases=%q max=%v",
				model.ModelID,
				model.DisplayModelID,
				model.DisplayName,
				model.DisplayNameShort,
				model.Aliases,
				model.MaxMode,
			)
		}
	}
}
