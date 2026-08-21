package cursor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"
)

// SandClientVersion matches the packaged Grok Bot/Sand client analyzed in
// this repository. Accounts may override it through AgentOptions.
const SandClientVersion = "0.20.0"

// 设备指纹与 x-cursor-checksum。
//
// 协议对照来源：反代 device.js 的 deriveTelemetryIds 与 cursor-agent-env.js 的
// buildOfficialHeaders/wZg。二者都是从 Cursor 客户端逆向出来的，移植后必须
// 与 Node 实现做逐字节对拍（见 device_test.go 的固定向量）。

// TelemetryIDs 是 checksum 需要的两个机器标识。
//
// 它们是从种子派生的，不依赖本机安装的 Cursor：服务器上没有 storage.json，
// 而同一个账号必须长期呈现同一台"设备"，否则上游风控会看到设备漂移。
type TelemetryIDs struct {
	// MachineID 是 64 位十六进制。
	MachineID string
	// MacMachineID 是 128 位十六进制（两段 sha256 拼接）。
	MacMachineID string
}

// DeriveTelemetryIDs 从种子派生设备标识。
// 种子建议用账号的 access token 或一个持久化的随机值，保证每账号稳定。
func DeriveTelemetryIDs(seed string) TelemetryIDs {
	if strings.TrimSpace(seed) == "" {
		seed = "anonymous"
	}
	base := sha256Hex("cursor-proxy:v1:" + seed)
	return TelemetryIDs{
		MachineID:    sha256Hex("machine:" + base),
		MacMachineID: sha256Hex("mac:"+base) + sha256Hex("mac2:"+base),
	}
}

func sha256Hex(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

// Checksum 构造 x-cursor-checksum 请求头的取值：
//
//	base64url(obfuscate(timestampBytes)) + machineId + "/" + macMachineId
func Checksum(ids TelemetryIDs, at time.Time) string {
	payload := obfuscateChecksumBytes(checksumTimestampBytes(at))
	// 输入恒为 6 字节（3 的倍数），所以标准 RawURLEncoding 与反代那份手写
	// base64 逐字符一致——它用的正是 base64url 字母表且不带 padding。
	return base64.RawURLEncoding.EncodeToString(payload) + ids.MachineID + "/" + ids.MacMachineID
}

// SandChecksum mirrors the Sand client's checksum:
//
//	base64url(obfuscate(timestampBytes)) + machineID
//
// Unlike the IDE checksum, Sand does not append a slash-separated telemetry
// pair. The timestamp encoding and rolling obfuscation are shared.
func SandChecksum(machineID string, at time.Time) string {
	payload := obfuscateChecksumBytes(checksumTimestampBytes(at))
	return base64.RawURLEncoding.EncodeToString(payload) + strings.TrimSpace(machineID)
}

// checksumTimestampBytes 复刻反代的 6 字节时间戳编码。
//
// 这里刻意保留了原实现的一个 JavaScript 语义细节：JS 的位移运算数会被
// 掩码到 5 位（n & 31），所以源码里的 `E >> 40` 实际执行的是 `E >> 8`，
// `E >> 32` 实际是 `E >> 0`。Go 会老老实实移 40 位得到 0，直译反而与
// 上游客户端不一致——Cursor 自己的客户端也是 JS，带着同样的行为。
func checksumTimestampBytes(at time.Time) []byte {
	// E = floor(Date.now() / 1e6)，约 21 位，稳稳落在 int32 范围内。
	e := uint32(at.UnixMilli() / 1e6)
	return []byte{
		byte(e >> 8), // 源码写的是 >> 40
		byte(e),      // 源码写的是 >> 32
		byte(e >> 24),
		byte(e >> 16),
		byte(e >> 8),
		byte(e),
	}
}

// obfuscateChecksumBytes 是反代里的 wZg：以 165 起始的滚动异或加位置偏移。
func obfuscateChecksumBytes(input []byte) []byte {
	out := make([]byte, len(input))
	prev := byte(165)
	for i, b := range input {
		out[i] = (b ^ prev) + byte(i%256)
		prev = out[i]
	}
	return out
}
