package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEnrichCodexModelsManifestAddsGPT56ProThinkingAndFast(t *testing.T) {
	body := []byte(`{"models":[{"slug":"gpt-5.6-sol"},{"slug":"gpt-5.5","display_name":"GPT-5.5"}]}`)

	got, err := enrichCodexModelsManifest(body)
	require.NoError(t, err)

	sol := gjson.GetBytes(got, `models.#(slug="gpt-5.6-sol")`)
	require.True(t, sol.Exists())
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max", "ultra"},
		codexManifestEffortValues(t, sol.Raw))
	require.Contains(t, gjson.Get(sol.Raw, "additional_speed_tiers").String(), "fast")
	require.Equal(t, "priority", gjson.Get(sol.Raw, "service_tiers.0.id").String())
	require.Equal(t, "Fast", gjson.Get(sol.Raw, "service_tiers.0.name").String())

	gpt55 := gjson.GetBytes(got, `models.#(slug="gpt-5.5")`)
	require.True(t, gpt55.Exists())
	require.Equal(t, []string{"low", "medium", "high", "xhigh"},
		codexManifestEffortValues(t, gpt55.Raw),
		"GPT-5.5 must not advertise GPT-5.6 Pro thinking levels")
	require.False(t, gjson.Get(gpt55.Raw, "additional_speed_tiers").Exists())
	require.False(t, gjson.Get(gpt55.Raw, "service_tiers").Exists())
}

func TestEnrichCodexModelsManifestAppendsMissingGPT56ProLevels(t *testing.T) {
	body := []byte(`{"models":[{"slug":"gpt-5.6-terra","supported_reasoning_levels":["low","medium","high","xhigh"]}]}`)

	got, err := enrichCodexModelsManifest(body)
	require.NoError(t, err)

	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max", "ultra"},
		codexManifestEffortValues(t, gjson.GetBytes(got, "models.0").Raw))
	require.Equal(t, "fast", gjson.GetBytes(got, "models.0.additional_speed_tiers.0").String())
}

func TestEnrichCodexModelsManifestPreservesCompleteGPT56Catalog(t *testing.T) {
	body := []byte(`{"models":[{
		"slug":"gpt-5.6-luna",
		"display_name":"Luna",
		"default_reasoning_level":"medium",
		"supported_in_api":true,
		"visibility":"list",
		"supported_reasoning_levels":[
			{"effort":"low","description":"custom-low"},
			{"effort":"medium","description":"custom-medium"},
			{"effort":"high","description":"custom-high"},
			{"effort":"xhigh","description":"custom-xhigh"},
			{"effort":"max","description":"custom-max"},
			{"effort":"ultra","description":"custom-ultra"}
		],
		"additional_speed_tiers":["fast"],
		"service_tiers":[{"id":"priority","name":"Fast","description":"already here"}]
	}]}`)

	got, err := enrichCodexModelsManifest(body)
	require.NoError(t, err)
	require.Equal(t, string(body), string(got))
}

func TestIsOpenAIGPT56FamilyModel(t *testing.T) {
	require.True(t, isOpenAIGPT56FamilyModel("gpt-5.6-sol"))
	require.True(t, isOpenAIGPT56FamilyModel("openai/gpt-5.6-terra"))
	require.True(t, isOpenAIGPT56FamilyModel("gpt-5.6-codex"))
	require.False(t, isOpenAIGPT56FamilyModel("gpt-5.5"))
	require.False(t, isOpenAIGPT56FamilyModel("gpt-5.5-pro"))
}

func TestRewriteOpenAIUpstreamReasoningEffortForGPT56(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","reasoning":{"effort":"ultra"}}`)
	got, changed, err := rewriteOpenAIUpstreamReasoningEffort(body, "gpt-5.6-sol")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "max", gjson.GetBytes(got, "reasoning.effort").String())

	kept, changed, err := rewriteOpenAIUpstreamReasoningEffort(
		[]byte(`{"reasoning":{"effort":"max"}}`), "gpt-5.6-sol")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, "max", gjson.GetBytes(kept, "reasoning.effort").String())

	downgraded, changed, err := rewriteOpenAIUpstreamReasoningEffort(
		[]byte(`{"reasoning":{"effort":"max"}}`), "gpt-5.5")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "xhigh", gjson.GetBytes(downgraded, "reasoning.effort").String())
}

func requireCodexManifestModelEfforts(t *testing.T, body []byte, slug string, want []string) {
	t.Helper()
	model := gjson.GetBytes(body, `models.#(slug="`+slug+`")`)
	require.True(t, model.Exists(), "missing model %s in %s", slug, body)
	require.Equal(t, want, codexManifestEffortValues(t, model.Raw))
}

func requireGPT56CodexPickerFields(t *testing.T, body []byte, slug string) {
	t.Helper()
	requireCodexManifestModelEfforts(t, body, slug, []string{"low", "medium", "high", "xhigh", "max", "ultra"})
	require.Equal(t, "fast", gjson.GetBytes(body, `models.#(slug="`+slug+`").additional_speed_tiers.0`).String())
}

func codexManifestEffortValues(t *testing.T, raw string) []string {
	t.Helper()
	levels := gjson.Get(raw, "supported_reasoning_levels")
	require.True(t, levels.Exists())
	values := make([]string, 0, len(levels.Array()))
	for _, level := range levels.Array() {
		if level.Type == gjson.String {
			values = append(values, level.String())
			continue
		}
		var parsed struct {
			Effort string `json:"effort"`
		}
		require.NoError(t, json.Unmarshal([]byte(level.Raw), &parsed))
		values = append(values, parsed.Effort)
	}
	return values
}
