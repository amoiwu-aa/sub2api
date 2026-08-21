package cursor

import (
	"context"
	"fmt"
	"strings"
)

// SandModelDetails is one model returned by AgentService/GetUsableModels.
type SandModelDetails struct {
	ModelID          string
	DisplayModelID   string
	DisplayName      string
	DisplayNameShort string
	Aliases          []string
	MaxMode          bool
}

// FetchSandUsableModels returns the model catalog available to one Grok Bot
// account. The upstream applies its Sand-specific model filter before returning
// this list, so callers should prefer it over the shared Cursor IDE catalog.
func FetchSandUsableModels(
	ctx context.Context,
	opts *Options,
	accessToken string,
	machineID string,
	clientVersion string,
	namespace string,
	customModelIDs []string,
) ([]SandModelDetails, error) {
	var payload []byte
	for _, modelID := range customModelIDs {
		if trimmed := strings.TrimSpace(modelID); trimmed != "" {
			payload = append(payload, EncodeStringField(1, trimmed)...)
		}
	}

	body, err := sandConnectUnary(
		ctx,
		opts,
		accessToken,
		machineID,
		clientVersion,
		namespace,
		sandAgentServicePath,
		"GetUsableModels",
		payload,
		false,
	)
	if err != nil {
		return nil, err
	}
	return decodeSandUsableModels(body)
}

func decodeSandUsableModels(data []byte) ([]SandModelDetails, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return nil, fmt.Errorf("decode Sand usable models: %w", err)
	}

	models := make([]SandModelDetails, 0)
	for _, field := range fields {
		if field.Number != 1 || field.WireType != wireBytes {
			continue
		}
		model, decodeErr := decodeSandModelDetails(field.Bytes)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if model.ModelID != "" {
			models = append(models, model)
		}
	}
	return models, nil
}

func decodeSandModelDetails(data []byte) (SandModelDetails, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return SandModelDetails{}, fmt.Errorf("decode Sand model details: %w", err)
	}

	model := SandModelDetails{
		ModelID:          strings.TrimSpace(FieldString(fields, 1)),
		DisplayModelID:   strings.TrimSpace(FieldString(fields, 3)),
		DisplayName:      strings.TrimSpace(FieldString(fields, 4)),
		DisplayNameShort: strings.TrimSpace(FieldString(fields, 5)),
		MaxMode:          sandBoolField(fields, 7),
	}
	for _, field := range fields {
		if field.Number == 6 && field.WireType == wireBytes {
			if alias := strings.TrimSpace(string(field.Bytes)); alias != "" {
				model.Aliases = append(model.Aliases, alias)
			}
		}
	}
	return model, nil
}

// SandPublicModelIDs converts the verbose Sand catalog into RingStar's compact
// cursor/... IDs. Unknown upstream entries are intentionally discarded.
func SandPublicModelIDs(models []SandModelDetails) []string {
	availableUpstreamIDs := make(map[string]struct{})
	for _, model := range models {
		for _, candidate := range []string{model.DisplayModelID, model.ModelID} {
			if modelID := matchSandSupportedModelID(candidate); modelID != "" {
				availableUpstreamIDs[modelID] = struct{}{}
				break
			}
		}
	}

	defaults := SandDefaultModels()
	publicIDs := make([]string, 0, len(defaults))
	for _, model := range defaults {
		selection, err := ResolveModelStrict(model.ID)
		if err != nil {
			continue
		}
		if _, ok := availableUpstreamIDs[selection.ModelID]; ok {
			publicIDs = append(publicIDs, model.ID)
		}
	}
	return publicIDs
}

func matchSandSupportedModelID(raw string) string {
	candidate := strings.ToLower(strings.TrimSpace(raw))
	candidate = strings.TrimPrefix(candidate, PublicModelPrefix)
	candidate = strings.TrimPrefix(candidate, "cursor-")
	if candidate == "" {
		return ""
	}
	if _, ok := sandSupportedUpstreamModelIDs[candidate]; ok {
		return candidate
	}

	// ModelID may include Sand's effort/fast variants while DisplayModelID is
	// absent. Match only a verified base followed by a variant separator.
	bestMatch := ""
	for modelID := range sandSupportedUpstreamModelIDs {
		if len(modelID) <= len(bestMatch) {
			continue
		}
		if strings.HasPrefix(candidate, modelID+"-") ||
			strings.HasPrefix(candidate, modelID+":") ||
			strings.HasPrefix(candidate, modelID+"/") {
			bestMatch = modelID
		}
	}
	return bestMatch
}
