package service

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// applyConvertedAnthropicCachePolicy adds protocol-native cache anchors after an
// OpenAI Chat Completions/Responses request has been converted to Anthropic.
//
// Stable prefixes (system and tools) use stableTTL, while rolling conversation
// anchors always use Anthropic's short default TTL. This keeps the 1h anchors
// before 5m anchors and avoids repeatedly writing the changing message tail at
// the more expensive long TTL.
func applyConvertedAnthropicCachePolicy(body []byte, stableTTL string) []byte {
	stableTTL = strings.TrimSpace(stableTTL)
	if stableTTL == "" {
		stableTTL = claude.DefaultCacheControlTTL
	}

	body = stripMessageCacheControl(body)
	body = applySystemLastCacheBreakpointWithTTL(body, stableTTL)
	body = applyToolsLastCacheBreakpointWithTTL(body, stableTTL)
	body = addMessageCacheBreakpoints(body)
	return enforceCacheControlLimit(body)
}

func applySystemLastCacheBreakpointWithTTL(body []byte, ttl string) []byte {
	system := gjson.GetBytes(body, "system")
	if !system.Exists() {
		return body
	}

	cacheControl := fmt.Sprintf(`{"type":"ephemeral","ttl":%q}`, ttl)
	if system.Type == gjson.String {
		block := fmt.Sprintf(
			`[{"type":"text","text":%s,"cache_control":%s}]`,
			mustJSONString(system.String()),
			cacheControl,
		)
		if next, err := sjson.SetRawBytes(body, "system", []byte(block)); err == nil {
			return next
		}
		return body
	}

	if !system.IsArray() {
		return body
	}
	blocks := system.Array()
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Get("type").String() == "thinking" {
			continue
		}
		path := fmt.Sprintf("system.%d.cache_control", i)
		if next, err := sjson.SetRawBytes(body, path, []byte(cacheControl)); err == nil {
			return next
		}
		return body
	}
	return body
}

func applyToolsLastCacheBreakpointWithTTL(body []byte, ttl string) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return body
	}
	arr := tools.Array()
	if len(arr) == 0 {
		return body
	}

	cacheControl := fmt.Sprintf(`{"type":"ephemeral","ttl":%q}`, ttl)
	path := fmt.Sprintf("tools.%d.cache_control", len(arr)-1)
	if next, err := sjson.SetRawBytes(body, path, []byte(cacheControl)); err == nil {
		return next
	}
	return body
}

func applyAnthropicCacheBreakdownToChatUsage(chatUsage *apicompat.ChatUsage, usage ClaudeUsage) {
	if chatUsage == nil || (usage.CacheCreation5mTokens <= 0 && usage.CacheCreation1hTokens <= 0) {
		return
	}
	if chatUsage.PromptTokensDetails == nil {
		chatUsage.PromptTokensDetails = &apicompat.ChatTokenDetails{}
	}
	chatUsage.PromptTokensDetails.CacheCreation5mTokens = usage.CacheCreation5mTokens
	chatUsage.PromptTokensDetails.CacheCreation1hTokens = usage.CacheCreation1hTokens
}
