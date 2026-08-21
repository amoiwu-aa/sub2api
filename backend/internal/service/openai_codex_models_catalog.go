package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Codex custom-provider catalogs often arrive as slug-only /v1/models rewrites.
// Codex then falls back to empty supported_reasoning_levels and empty
// service_tiers, which hides Pro-only thinking levels (max / ultra) and Fast.
// These helpers fill those picker fields without dropping unknown upstream data.

type codexCatalogReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type codexCatalogServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type codexCatalogSpec struct {
	DisplayName        string
	DefaultReasoning   string
	ReasoningLevels    []codexCatalogReasoningLevel
	AdvertiseFast      bool
	SupportedInAPI     bool
	Visibility         string
}

const (
	codexCatalogFastTierID          = "priority"
	codexCatalogFastSpeedTier       = "fast"
	codexCatalogFastTierName        = "Fast"
	codexCatalogFastTierDescription = "1.5x speed, increased usage"
)

func codexStandardReasoningLevels() []codexCatalogReasoningLevel {
	return []codexCatalogReasoningLevel{
		{Effort: "low", Description: "Faster responses with lighter reasoning"},
		{Effort: "medium", Description: "Balances speed and reasoning depth"},
		{Effort: "high", Description: "Harder problems with deeper reasoning"},
		{Effort: "xhigh", Description: "Extra high reasoning for complex work"},
	}
}

func codexProReasoningLevels() []codexCatalogReasoningLevel {
	return append(codexStandardReasoningLevels(),
		codexCatalogReasoningLevel{Effort: "max", Description: "Maximum reasoning for the hardest tasks"},
		codexCatalogReasoningLevel{Effort: "ultra", Description: "Maximum reasoning with multi-agent delegation"},
	)
}

func enrichCodexModelsManifest(body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}
	var models []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, fmt.Errorf("decode top-level models array: %w", err)
	}

	changed := false
	for i, rawModel := range models {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(rawModel, &model); err != nil || model == nil {
			continue
		}
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil {
			continue
		}
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}
		spec := codexModelCatalogSpec(slug)
		if spec == nil {
			continue
		}
		modelChanged, err := applyCodexModelCatalogSpec(model, spec)
		if err != nil {
			return nil, fmt.Errorf("enrich model %q: %w", slug, err)
		}
		if !modelChanged {
			continue
		}
		adjusted, err := json.Marshal(model)
		if err != nil {
			return nil, fmt.Errorf("encode model %q: %w", slug, err)
		}
		models[i] = adjusted
		changed = true
	}
	if !changed {
		return body, nil
	}

	adjustedModels, err := json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("encode top-level models array: %w", err)
	}
	envelope["models"] = adjustedModels
	adjusted, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode JSON object: %w", err)
	}
	return adjusted, nil
}

func applyCodexModelCatalogSpec(model map[string]json.RawMessage, spec *codexCatalogSpec) (bool, error) {
	changed := false

	if spec.DisplayName != "" && !jsonRawStringPresent(model["display_name"]) {
		encoded, err := json.Marshal(spec.DisplayName)
		if err != nil {
			return false, err
		}
		model["display_name"] = encoded
		changed = true
	}
	if spec.DefaultReasoning != "" && !jsonRawStringPresent(model["default_reasoning_level"]) {
		encoded, err := json.Marshal(spec.DefaultReasoning)
		if err != nil {
			return false, err
		}
		model["default_reasoning_level"] = encoded
		changed = true
	}
	if spec.Visibility != "" && !jsonRawStringPresent(model["visibility"]) {
		encoded, err := json.Marshal(spec.Visibility)
		if err != nil {
			return false, err
		}
		model["visibility"] = encoded
		changed = true
	}
	if spec.SupportedInAPI && !jsonRawBoolTrue(model["supported_in_api"]) && !jsonRawPresent(model["supported_in_api"]) {
		model["supported_in_api"] = json.RawMessage("true")
		changed = true
	}

	mergedLevels, levelsChanged, err := mergeCodexReasoningLevels(model["supported_reasoning_levels"], spec.ReasoningLevels)
	if err != nil {
		return false, err
	}
	if levelsChanged {
		encoded, err := json.Marshal(mergedLevels)
		if err != nil {
			return false, err
		}
		model["supported_reasoning_levels"] = encoded
		changed = true
	}

	if spec.AdvertiseFast {
		fastChanged, err := ensureCodexFastSpeedTiers(model)
		if err != nil {
			return false, err
		}
		changed = changed || fastChanged
	}
	return changed, nil
}

func mergeCodexReasoningLevels(raw json.RawMessage, wanted []codexCatalogReasoningLevel) ([]codexCatalogReasoningLevel, bool, error) {
	existing, err := decodeCodexReasoningLevels(raw)
	if err != nil {
		return nil, false, err
	}
	if len(wanted) == 0 {
		return existing, false, nil
	}
	seen := make(map[string]struct{}, len(existing)+len(wanted))
	merged := make([]codexCatalogReasoningLevel, 0, len(existing)+len(wanted))
	for _, level := range existing {
		effort := strings.ToLower(strings.TrimSpace(level.Effort))
		if effort == "" {
			continue
		}
		if _, ok := seen[effort]; ok {
			continue
		}
		seen[effort] = struct{}{}
		if strings.TrimSpace(level.Description) == "" {
			level.Description = codexReasoningLevelDescription(effort)
		}
		level.Effort = effort
		merged = append(merged, level)
	}
	changed := false
	if len(merged) == 0 {
		return append([]codexCatalogReasoningLevel(nil), wanted...), true, nil
	}
	for _, level := range wanted {
		if _, ok := seen[level.Effort]; ok {
			continue
		}
		seen[level.Effort] = struct{}{}
		merged = append(merged, level)
		changed = true
	}
	return merged, changed, nil
}

func decodeCodexReasoningLevels(raw json.RawMessage) ([]codexCatalogReasoningLevel, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var objects []codexCatalogReasoningLevel
	if err := json.Unmarshal(raw, &objects); err == nil {
		return objects, nil
	}
	var efforts []string
	if err := json.Unmarshal(raw, &efforts); err != nil {
		return nil, fmt.Errorf("decode supported_reasoning_levels: %w", err)
	}
	out := make([]codexCatalogReasoningLevel, 0, len(efforts))
	for _, effort := range efforts {
		effort = strings.ToLower(strings.TrimSpace(effort))
		if effort == "" {
			continue
		}
		out = append(out, codexCatalogReasoningLevel{
			Effort:      effort,
			Description: codexReasoningLevelDescription(effort),
		})
	}
	return out, nil
}

func ensureCodexFastSpeedTiers(model map[string]json.RawMessage) (bool, error) {
	changed := false

	speedTiers, err := decodeStringArray(model["additional_speed_tiers"])
	if err != nil {
		return false, err
	}
	if !stringSliceContainsFold(speedTiers, codexCatalogFastSpeedTier) {
		speedTiers = append(speedTiers, codexCatalogFastSpeedTier)
		encoded, err := json.Marshal(speedTiers)
		if err != nil {
			return false, err
		}
		model["additional_speed_tiers"] = encoded
		changed = true
	}

	serviceTiers, err := decodeCodexServiceTiers(model["service_tiers"])
	if err != nil {
		return false, err
	}
	if !codexServiceTiersAdvertiseFast(serviceTiers) {
		serviceTiers = append(serviceTiers, codexCatalogServiceTier{
			ID:          codexCatalogFastTierID,
			Name:        codexCatalogFastTierName,
			Description: codexCatalogFastTierDescription,
		})
		encoded, err := json.Marshal(serviceTiers)
		if err != nil {
			return false, err
		}
		model["service_tiers"] = encoded
		changed = true
	}
	return changed, nil
}

func decodeCodexServiceTiers(raw json.RawMessage) ([]codexCatalogServiceTier, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var tiers []codexCatalogServiceTier
	if err := json.Unmarshal(raw, &tiers); err != nil {
		return nil, fmt.Errorf("decode service_tiers: %w", err)
	}
	return tiers, nil
}

func decodeStringArray(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("decode string array: %w", err)
	}
	return values, nil
}

func codexServiceTiersAdvertiseFast(tiers []codexCatalogServiceTier) bool {
	for _, tier := range tiers {
		id := strings.ToLower(strings.TrimSpace(tier.ID))
		if id == codexCatalogFastTierID || id == codexCatalogFastSpeedTier {
			return true
		}
	}
	return false
}

func codexModelCatalogSpec(slug string) *codexCatalogSpec {
	normalized := strings.ToLower(lastOpenAIModelSegment(slug))
	if normalized == "" || !codexModelShouldAdvertisePickerCapabilities(normalized) {
		return nil
	}
	spec := &codexCatalogSpec{
		DisplayName:      codexCatalogDisplayName(normalized),
		DefaultReasoning: "medium",
		ReasoningLevels:  codexStandardReasoningLevels(),
		AdvertiseFast:    codexModelShouldAdvertiseFast(normalized),
		SupportedInAPI:   true,
		Visibility:       "list",
	}
	if isOpenAIGPT56FamilyModel(normalized) {
		spec.ReasoningLevels = codexProReasoningLevels()
	}
	return spec
}

func codexModelShouldAdvertisePickerCapabilities(model string) bool {
	switch {
	case strings.HasPrefix(model, "gpt-image"),
		model == "codex-auto-review",
		strings.Contains(model, "realtime"),
		strings.Contains(model, "audio-preview"):
		return false
	case strings.HasPrefix(model, "gpt-5"),
		strings.Contains(model, "codex"):
		return true
	default:
		return false
	}
}

func codexModelShouldAdvertiseFast(model string) bool {
	return isOpenAIGPT56FamilyModel(model)
}

func codexCatalogDisplayName(model string) string {
	switch model {
	case "gpt-5.6", "gpt-5.6-sol":
		return "GPT-5.6 Sol"
	case "gpt-5.6-sol-wm":
		return "GPT-5.6 Sol WM"
	case "gpt-5.6-terra":
		return "GPT-5.6 Terra"
	case "gpt-5.6-luna":
		return "GPT-5.6 Luna"
	case "gpt-5.5-pro":
		return "GPT-5.5 Pro"
	case "gpt-5.5":
		return "GPT-5.5"
	case "gpt-5.4-pro":
		return "GPT-5.4 Pro"
	case "gpt-5.4-mini":
		return "GPT-5.4 Mini"
	case "gpt-5.4":
		return "GPT-5.4"
	case "gpt-5.3-codex-spark":
		return "GPT-5.3 Codex Spark"
	case "gpt-5.3-codex":
		return "GPT-5.3 Codex"
	case "gpt-5.2-pro":
		return "GPT-5.2 Pro"
	case "gpt-5.2":
		return "GPT-5.2"
	default:
		return model
	}
}

func codexReasoningLevelDescription(effort string) string {
	for _, level := range codexProReasoningLevels() {
		if level.Effort == effort {
			return level.Description
		}
	}
	return "RingStar Codex reasoning effort " + effort
}

func jsonRawPresent(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null"))
}

func jsonRawStringPresent(raw json.RawMessage) bool {
	if !jsonRawPresent(raw) {
		return false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return true
	}
	return strings.TrimSpace(value) != ""
}

func jsonRawBoolTrue(raw json.RawMessage) bool {
	if !jsonRawPresent(raw) {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value
}

func stringSliceContainsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), wanted) {
			return true
		}
	}
	return false
}
