package kiro

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelIDExistsOnEveryTier(t *testing.T) {
	// auto 是唯一在免费档与企业档都存在的档位，也是上游自己返回的 defaultModel。
	// 换成具体模型会让某一档的账号第一个请求就吃 INVALID_MODEL_ID。
	require.Equal(t, "auto", DefaultModelID)
	require.Contains(t, DefaultModelIDs(), PublicModelPrefix+DefaultModelID)
}

func TestDefaultModelsCoverBothTiers(t *testing.T) {
	ids := DefaultModelIDs()
	// 免费档实测有的
	for _, id := range []string{
		"kiro/auto", "kiro/claude-sonnet-4.5", "kiro/claude-sonnet-4",
		"kiro/claude-haiku-4.5", "kiro/deepseek-3.2", "kiro/minimax-m2.5",
		"kiro/minimax-m2.1", "kiro/glm-5", "kiro/qwen3-coder-next",
	} {
		require.Contains(t, ids, id, "免费档模型缺失")
	}
	// 企业档才有的也要留着：静态目录是并集，不是某一档的清单
	for _, id := range []string{"kiro/claude-sonnet-4.6", "kiro/claude-opus-4.6"} {
		require.Contains(t, ids, id, "企业档模型缺失")
	}
	for _, id := range ids {
		require.True(t, len(id) > len(PublicModelPrefix) && id[:len(PublicModelPrefix)] == PublicModelPrefix,
			"模型 id 必须带 kiro/ 前缀，否则会被判成 anthropic 调度到 Claude 账号池: %s", id)
	}
}

func TestModelsFromCatalogUsesUpstreamList(t *testing.T) {
	catalog := NewCatalog([]AvailableModel{
		{ModelID: "claude-sonnet-4.5", ModelName: "Claude Sonnet 4.5", RateMultiplier: 1.3},
		{ModelID: "qwen3-coder-next", ModelName: "Qwen3 Coder Next", RateMultiplier: 0.05},
		{ModelID: "auto", ModelName: "Auto", RateMultiplier: 0.5},
	}, time.Now())

	models := ModelsFromCatalog(catalog)
	require.Len(t, models, 3)
	// Catalog 按计费倍率升序，便宜的在前
	require.Equal(t, "kiro/qwen3-coder-next", models[0].ID)
	require.Equal(t, "kiro/auto", models[1].ID)
	require.Equal(t, "kiro/claude-sonnet-4.5", models[2].ID)

	require.Equal(t, "Kiro Qwen3 Coder Next", models[0].DisplayName)
	require.Equal(t, "amazon", models[0].OwnedBy)
	require.Equal(t, "model", models[0].Object)
}

func TestModelsFromCatalogFallsBackToModelIDForDisplay(t *testing.T) {
	catalog := NewCatalog([]AvailableModel{{ModelID: "glm-5"}}, time.Now())
	models := ModelsFromCatalog(catalog)
	require.Len(t, models, 1)
	require.Equal(t, "Kiro glm-5", models[0].DisplayName)
}

func TestModelsFromCatalogNilAndEmpty(t *testing.T) {
	// 调用方靠 nil 判断「目录未就绪」，进而退回静态目录；不能返回空切片糊弄过去。
	require.Nil(t, ModelsFromCatalog(nil))
	require.Nil(t, ModelsFromCatalog(NewCatalog(nil, time.Now())))
}
