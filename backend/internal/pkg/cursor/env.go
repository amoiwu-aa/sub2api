package cursor

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// 少量运行期可调参数走环境变量。
//
// 这些值都是对着真实上游量出来的经验值，换个账号或换个上游模型就可能要重调。
// 走环境变量是为了让线上不必重新构建镜像就能试新值；取不到或取不出正数时
// 一律回落到默认值，配错不至于让网关起不来。

func envDuration(key string, fallback time.Duration, unit time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * unit
}

func envBytes(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}
