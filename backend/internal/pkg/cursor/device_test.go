package cursor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 本文件的期望值由 testdata/checksum_reference.js 生成——那份脚本是从反代
// device.js / cursor-agent-env.js 原样摘出来的。checksum 是逆向算法，
// 任何"看起来更对"的改写都必须先让这些向量继续通过。

func TestDeriveTelemetryIDsMatchesNodeReference(t *testing.T) {
	cases := []struct {
		seed         string
		machineID    string
		macMachineID string
	}{
		{
			// 空种子回落到 "anonymous"，两者必须得到同一台"设备"。
			seed:         "",
			machineID:    "9c5cf32fcc7a726f87ff71c0aa77e8e9587c121d298a4157669e6ac611ecde2d",
			macMachineID: "d5fe44e4c0cd13193182fa5335a7815b51ab8ba03b3965f0beb746a85e3fd91ca24f6291e3f1c34ece8ddb9a141aca33f6b6bbc39d136d4c93c8a7aa08baf037",
		},
		{
			seed:         "anonymous",
			machineID:    "9c5cf32fcc7a726f87ff71c0aa77e8e9587c121d298a4157669e6ac611ecde2d",
			macMachineID: "d5fe44e4c0cd13193182fa5335a7815b51ab8ba03b3965f0beb746a85e3fd91ca24f6291e3f1c34ece8ddb9a141aca33f6b6bbc39d136d4c93c8a7aa08baf037",
		},
		{
			seed:         "account-42",
			machineID:    "ab9badfb88853affa9d1541dd24f245192e06ed80310812657e38f4eaea8faed",
			macMachineID: "f735b8c6fac0540cb242629d453fbbdcf0e77438c059b51e3b15540595a97c3105ad98da4e2823670ee2d9e8dee6cb5ac8eaf7e908fff2ab167f91c7d68afcd8",
		},
		{
			seed:         "eyJhbGciOiJIUzI1NiJ9.payload.sig",
			machineID:    "dbb5e2bb64b5a2a1677b69117fd064f02fd0c0433b5d926a4dd9cbe761713f2c",
			macMachineID: "3aa331b4c0523e28146a00bc0edf98795393cfd7b55ab04f5e2d4174ba85d6cc0f8352264e8d35d5ab8b185f409afd5ddf2401111fd3100a445262c004fb5d50",
		},
	}

	for _, tc := range cases {
		ids := DeriveTelemetryIDs(tc.seed)
		require.Equal(t, tc.machineID, ids.MachineID, "seed=%q", tc.seed)
		require.Equal(t, tc.macMachineID, ids.MacMachineID, "seed=%q", tc.seed)
		require.Len(t, ids.MachineID, 64)
		require.Len(t, ids.MacMachineID, 128)
	}
}

// JS 的位移运算数被掩码到 5 位，所以反代里的 `E >> 40` 其实是 `E >> 8`、
// `E >> 32` 其实是 `E >> 0`。Go 直译会得到 0，字节流就和上游客户端对不上。
// 这些向量把那个怪癖钉死。
func TestChecksumTimestampBytesReplicateJavaScriptShiftSemantics(t *testing.T) {
	cases := []struct {
		unixMilli int64
		expected  []byte
	}{
		{unixMilli: 0, expected: []byte{0, 0, 0, 0, 0, 0}},
		{unixMilli: 1, expected: []byte{0, 0, 0, 0, 0, 0}},
		{unixMilli: 1_000_000, expected: []byte{0, 1, 0, 0, 0, 1}},
		{unixMilli: 1_769_000_000_000, expected: []byte{254, 40, 0, 26, 254, 40}},
		{unixMilli: 1_774_519_200_000, expected: []byte{19, 183, 0, 27, 19, 183}},
		{unixMilli: 2_000_000_000_000, expected: []byte{132, 128, 0, 30, 132, 128}},
	}
	for _, tc := range cases {
		got := checksumTimestampBytes(time.UnixMilli(tc.unixMilli))
		require.Equal(t, tc.expected, got, "unixMilli=%d", tc.unixMilli)
	}
}

func TestObfuscateChecksumBytesMatchesNodeReference(t *testing.T) {
	cases := []struct {
		input    []byte
		expected []byte
	}{
		{input: []byte{0, 0, 0, 0, 0, 0}, expected: []byte{165, 166, 168, 171, 175, 180}},
		{input: []byte{0, 1, 0, 0, 0, 1}, expected: []byte{165, 165, 167, 170, 174, 180}},
		{input: []byte{254, 40, 0, 26, 254, 40}, expected: []byte{91, 116, 118, 111, 149, 194}},
		{input: []byte{19, 183, 0, 27, 19, 183}, expected: []byte{182, 2, 4, 34, 53, 135}},
		{input: []byte{132, 128, 0, 30, 132, 128}, expected: []byte{33, 162, 164, 189, 61, 194}},
	}
	for _, tc := range cases {
		require.Equal(t, tc.expected, obfuscateChecksumBytes(tc.input), "input=%v", tc.input)
	}

	// 就地修改会污染调用方的切片，checksum 每次请求都要重算。
	input := []byte{1, 2, 3}
	_ = obfuscateChecksumBytes(input)
	require.Equal(t, []byte{1, 2, 3}, input)
}

func TestChecksumMatchesNodeReference(t *testing.T) {
	ids := DeriveTelemetryIDs("account-42")
	suffix := ids.MachineID + "/" + ids.MacMachineID

	cases := map[int64]string{
		0:                 "paaoq6-0",
		1:                 "paaoq6-0",
		1_000_000:         "paWnqq60",
		1_769_000_000_000: "W3R2b5XC",
		1_774_519_200_000: "tgIEIjWH",
		2_000_000_000_000: "IaKkvT3C",
	}
	for unixMilli, prefix := range cases {
		require.Equal(t, prefix+suffix, Checksum(ids, time.UnixMilli(unixMilli)), "unixMilli=%d", unixMilli)
	}
}

func TestChecksumIsStableWithinTheSameMillionMillisecondBucket(t *testing.T) {
	ids := DeriveTelemetryIDs("account-42")
	// 取一个正好落在桶边界上的时刻（1774519 × 1e6）。
	base := int64(1_774_519_000_000)

	// E = floor(ms / 1e6)，所以同一个百万毫秒桶（约 16.7 分钟）内 checksum 不变。
	require.Equal(t, Checksum(ids, time.UnixMilli(base)), Checksum(ids, time.UnixMilli(base+999_999)))
	require.NotEqual(t, Checksum(ids, time.UnixMilli(base)), Checksum(ids, time.UnixMilli(base+1_000_000)))
}
