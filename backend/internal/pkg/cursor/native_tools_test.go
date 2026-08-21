package cursor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// nativeExecMessage 构造一条内置工具 exec 帧：exec_server_message(2) →
// arg 字段（read=7 / grep=5 / ls=8），入参结构取自反代 parseExecServerMessage。
func nativeExecMessage(id uint64, execID string, argField int, argPayload []byte) []byte {
	return EncodeBytesField(2, concat(
		EncodeVarintField(1, id),
		EncodeStringField(15, execID),
		EncodeBytesField(argField, argPayload),
	))
}

func nativeReadArgsPayload(path, toolCallID string, offset, limit uint64) []byte {
	parts := [][]byte{EncodeStringField(1, path)}
	if toolCallID != "" {
		parts = append(parts, EncodeStringField(2, toolCallID))
	}
	if offset > 0 {
		parts = append(parts, EncodeVarintField(4, offset))
	}
	if limit > 0 {
		parts = append(parts, EncodeVarintField(5, limit))
	}
	return concat(parts...)
}

func nativeWriteArgsPayload(path, content, toolCallID string) []byte {
	parts := [][]byte{
		EncodeStringField(1, path),
		EncodeStringField(2, content),
	}
	if toolCallID != "" {
		parts = append(parts, EncodeStringField(3, toolCallID))
	}
	return concat(parts...)
}

// nativeBridgeOf 把「内置名 → 客户端工具名」的简写展开成桥接映射。
// 只关心名字映射的用例用它；参数改写由 ArgNames 专门的用例覆盖。
func nativeBridgeOf(mapping map[string]string) NativeToolBridge {
	bridge := make(NativeToolBridge, len(mapping))
	for key, name := range mapping {
		bridge[key] = NativeToolTarget{Name: name}
	}
	return bridge
}

func testNativeBridge() NativeToolBridge {
	return nativeBridgeOf(map[string]string{"read": "Read", "grep": "Grep", "ls": "LS"})
}

func TestTranslateNativeExecReadCarriesRangeAndCallID(t *testing.T) {
	message, err := ParseServerMessage(nativeExecMessage(3, "exec-9", 7,
		nativeReadArgsPayload("章节/034.md", "toolu_01", 120, 40)))
	require.NoError(t, err)
	require.Equal(t, KindExec, message.Kind)
	require.Equal(t, "read", message.Exec.Kind)

	call := TranslateNativeExec(testNativeBridge(), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Read", call.Name)
	// 上游入参里自带 tool_call_id，比 exec id 更贴近客户端语义，优先用它。
	require.Equal(t, "toolu_01", call.CallID)
	require.JSONEq(t, `{"path":"章节/034.md","offset":120,"limit":40}`, string(call.Arguments))
}

// ArgNames 非空时按客户端 schema 的属性名写出入参：Claude Code 的 Read 收
// file_path，原样发 path 过去会被它当成缺参数。
func TestTranslateNativeExecRenamesArgsForClient(t *testing.T) {
	message, err := ParseServerMessage(nativeExecMessage(3, "exec-rename", 7,
		nativeReadArgsPayload("章节/034.md", "toolu_01", 120, 40)))
	require.NoError(t, err)

	bridge := NativeToolBridge{"read": {
		Name:     "Read",
		ArgNames: map[string]string{"path": "file_path"},
	}}
	call := TranslateNativeExec(bridge, message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Read", call.Name)
	// 没有改写条目的入参保持规范名。
	require.JSONEq(t, `{"file_path":"章节/034.md","offset":120,"limit":40}`, string(call.Arguments))
}

// 可选入参为零值时不写出，改写表不该把它们凭空带出来。
func TestTranslateNativeExecRenameKeepsOptionalOmission(t *testing.T) {
	message, err := ParseServerMessage(nativeExecMessage(4, "exec-rename-2", 2,
		EncodeStringField(1, "npm test")))
	require.NoError(t, err)

	bridge := NativeToolBridge{"shell": {
		Name:     "Bash",
		ArgNames: map[string]string{"cwd": "working_directory"},
	}}
	call := TranslateNativeExec(bridge, message.Exec)
	require.NotNil(t, call)
	require.JSONEq(t, `{"command":"npm test"}`, string(call.Arguments))
}

func TestTranslateNativeExecReadFallsBackToExecID(t *testing.T) {
	message, err := ParseServerMessage(nativeExecMessage(3, "exec-fallback", 7,
		nativeReadArgsPayload("a.md", "", 0, 0)))
	require.NoError(t, err)

	call := TranslateNativeExec(testNativeBridge(), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "exec-fallback", call.CallID)
	require.JSONEq(t, `{"path":"a.md"}`, string(call.Arguments))
}

func TestTranslateNativeExecGrep(t *testing.T) {
	argPayload := concat(
		EncodeStringField(1, "交合|双修"),
		EncodeStringField(2, "章节"),
		EncodeStringField(3, "*.md"),
		EncodeStringField(4, "content"),
		EncodeVarintField(8, 1),
		EncodeVarintField(10, 50),
		EncodeStringField(14, "toolu_02"),
	)
	message, err := ParseServerMessage(nativeExecMessage(4, "exec-10", 5, argPayload))
	require.NoError(t, err)
	require.Equal(t, "grep", message.Exec.Kind)

	call := TranslateNativeExec(testNativeBridge(), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Grep", call.Name)
	require.Equal(t, "toolu_02", call.CallID)
	require.JSONEq(t, `{
		"pattern": "交合|双修",
		"path": "章节",
		"glob": "*.md",
		"output_mode": "content",
		"case_insensitive": true,
		"head_limit": 50
	}`, string(call.Arguments))
}

// Cursor 的 Glob 在 wire 上就是一次不带 pattern 的 grep。实测帧（2026-08-14，
// 账号 ultra，TestLiveSpikeNamedToolForensics）：
//
//	#5 { #3 "**/*.py"  #4 "files_with_matches"  #14 <tool_call_id> }
//
// 照 grep 翻译会给客户端发空 pattern，Grep 工具普遍要求它非空。
func TestTranslateNativeExecRoutesGlobShapedGrepToGlobTool(t *testing.T) {
	argPayload := concat(
		EncodeStringField(3, "**/*.py"),
		EncodeStringField(4, "files_with_matches"),
		EncodeStringField(14, "toolu_glob"),
	)
	message, err := ParseServerMessage(nativeExecMessage(20, "exec-glob", 5, argPayload))
	require.NoError(t, err)
	require.Equal(t, "grep", message.Exec.Kind, "wire 上仍然是 grep 字段")

	call := TranslateNativeExec(nativeBridgeOf(map[string]string{
		"grep": "Grep", "glob": "Glob",
	}), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Glob", call.Name)
	require.Equal(t, "toolu_glob", call.CallID)
	// 客户端 Glob 要的 pattern 是文件名匹配式，不是空正则。
	require.JSONEq(t, `{"pattern":"**/*.py"}`, string(call.Arguments))
}

// 带 path 的 glob：wire 的 #2 是搜索根目录。
func TestTranslateNativeExecGlobCarriesSearchRoot(t *testing.T) {
	argPayload := concat(
		EncodeStringField(2, "/home/cursor"),
		EncodeStringField(3, "**/*.md"),
		EncodeStringField(4, "files_with_matches"),
	)
	message, err := ParseServerMessage(nativeExecMessage(21, "exec-glob-2", 5, argPayload))
	require.NoError(t, err)

	call := TranslateNativeExec(nativeBridgeOf(map[string]string{"glob": "Glob"}), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "exec-glob-2", call.CallID)
	require.JSONEq(t, `{"pattern":"**/*.md","path":"/home/cursor"}`, string(call.Arguments))
}

// 客户端没声明 Glob 时不能退回 Grep：空 pattern 会让严格客户端当场失败。
func TestTranslateNativeExecGlobDoesNotFallBackToGrepTool(t *testing.T) {
	argPayload := concat(
		EncodeStringField(3, "**/*.py"),
		EncodeStringField(4, "files_with_matches"),
	)
	message, err := ParseServerMessage(nativeExecMessage(22, "exec-glob-3", 5, argPayload))
	require.NoError(t, err)

	call := TranslateNativeExec(nativeBridgeOf(map[string]string{"grep": "Grep"}), message.Exec)
	require.Nil(t, call)
}

// 带 pattern 的正常 grep 不受影响，仍然走 grep。
func TestTranslateNativeExecKeepsRealGrepOnGrepTool(t *testing.T) {
	argPayload := concat(
		EncodeStringField(1, "func main"),
		EncodeStringField(3, "*.go"),
	)
	message, err := ParseServerMessage(nativeExecMessage(23, "exec-grep-real", 5, argPayload))
	require.NoError(t, err)

	call := TranslateNativeExec(nativeBridgeOf(map[string]string{
		"grep": "Grep", "glob": "Glob",
	}), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Grep", call.Name)
	require.JSONEq(t, `{"pattern":"func main","glob":"*.go"}`, string(call.Arguments))
}

func TestTranslateNativeExecLsWithEmptyArgsStillBridges(t *testing.T) {
	// proto3 全默认值的消息是零字节：列默认目录的 ls 就长这样，
	// 必须转发（空参数）而不是回 stub。
	message, err := ParseServerMessage(nativeExecMessage(5, "exec-11", 8, nil))
	require.NoError(t, err)
	require.Equal(t, "ls", message.Exec.Kind)

	call := TranslateNativeExec(testNativeBridge(), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "LS", call.Name)
	require.Equal(t, "exec-11", call.CallID)
	require.JSONEq(t, `{"path":""}`, string(call.Arguments))
}

func TestTranslateNativeExecRedactedReadSharesReadMapping(t *testing.T) {
	message, err := ParseServerMessage(nativeExecMessage(6, "exec-12", 29,
		nativeReadArgsPayload("/etc/hosts", "toolu_03", 0, 0)))
	require.NoError(t, err)
	require.Equal(t, "redacted_read", message.Exec.Kind)

	call := TranslateNativeExec(testNativeBridge(), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Read", call.Name)
}

func TestTranslateNativeExecRefusesUnbridgedKinds(t *testing.T) {
	// subagent_await 这类非桥接 kind：映射表怎么配都不放行。
	await, err := ParseServerMessage(nativeExecMessage(7, "exec-13", 37,
		EncodeStringField(1, "x")))
	require.NoError(t, err)
	require.Nil(t, TranslateNativeExec(nativeBridgeOf(map[string]string{"read": "Read"}), await.Exec))

	read, err := ParseServerMessage(nativeExecMessage(8, "exec-14", 7,
		nativeReadArgsPayload("a.md", "", 0, 0)))
	require.NoError(t, err)
	require.Nil(t, TranslateNativeExec(nil, read.Exec), "未配置映射必须回落 stub")
	require.Nil(t, TranslateNativeExec(nativeBridgeOf(map[string]string{"grep": "Grep"}), read.Exec),
		"未映射的内置工具必须回落 stub")

	// shell 在白名单里，但映射没配 shell 键时同样回落 stub。
	shell, err := ParseServerMessage(nativeExecMessage(9, "exec-15", 2,
		EncodeStringField(1, "ls")))
	require.NoError(t, err)
	require.Nil(t, TranslateNativeExec(nativeBridgeOf(map[string]string{"read": "Read"}), shell.Exec))
}

func TestTranslateNativeExecShellCarriesBackgroundAndCwd(t *testing.T) {
	argPayload := concat(
		EncodeStringField(1, "npm run build"),
		EncodeStringField(2, "/srv/app"),
		EncodeVarintField(3, 120000),
		EncodeStringField(4, "toolu_31"),
		EncodeVarintField(11, 1),
		EncodeStringField(15, "build the app"),
	)
	message, err := ParseServerMessage(nativeExecMessage(10, "exec-16", 2, argPayload))
	require.NoError(t, err)
	require.Equal(t, "shell", message.Exec.Kind)

	call := TranslateNativeExec(nativeBridgeOf(map[string]string{"shell": "Bash"}), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Bash", call.Name)
	require.Equal(t, "toolu_31", call.CallID)
	require.JSONEq(t, `{
		"command": "npm run build",
		"cwd": "/srv/app",
		"timeout": 120000,
		"run_in_background": true,
		"description": "build the app"
	}`, string(call.Arguments))
}

func TestTranslateNativeExecShellStreamSharesShellMapping(t *testing.T) {
	// shell_stream 只是回执侧的流式变体，请求入参与 shell 一致。
	message, err := ParseServerMessage(nativeExecMessage(11, "exec-17", 14,
		EncodeStringField(1, "tail -f app.log")))
	require.NoError(t, err)
	require.Equal(t, "shell_stream", message.Exec.Kind)

	call := TranslateNativeExec(nativeBridgeOf(map[string]string{"shell": "Bash"}), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Bash", call.Name)
	require.JSONEq(t, `{"command":"tail -f app.log"}`, string(call.Arguments))
}

func TestTranslateNativeExecWriteDeleteFetch(t *testing.T) {
	bridge := nativeBridgeOf(map[string]string{"write": "Write", "delete": "Delete", "fetch": "WebFetch"})

	write, err := ParseServerMessage(nativeExecMessage(12, "exec-18", 3, concat(
		EncodeStringField(1, "章节/050.md"),
		EncodeStringField(2, "第五十章正文"),
		EncodeStringField(3, "toolu_41"),
	)))
	require.NoError(t, err)
	call := TranslateNativeExec(bridge, write.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Write", call.Name)
	require.Equal(t, "toolu_41", call.CallID)
	require.JSONEq(t, `{"path":"章节/050.md","content":"第五十章正文"}`, string(call.Arguments))

	del, err := ParseServerMessage(nativeExecMessage(13, "exec-19", 4, concat(
		EncodeStringField(1, "tmp/draft.md"),
		EncodeStringField(2, "toolu_42"),
	)))
	require.NoError(t, err)
	call = TranslateNativeExec(bridge, del.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Delete", call.Name)
	require.Equal(t, "toolu_42", call.CallID)
	require.JSONEq(t, `{"path":"tmp/draft.md"}`, string(call.Arguments))

	fetch, err := ParseServerMessage(nativeExecMessage(14, "exec-20", 20, concat(
		EncodeStringField(1, "https://example.com/ref"),
		EncodeStringField(2, "toolu_43"),
	)))
	require.NoError(t, err)
	require.Equal(t, "fetch", fetch.Exec.Kind, "fetch 必须被 execArgFields 识别")
	call = TranslateNativeExec(bridge, fetch.Exec)
	require.NotNil(t, call)
	require.Equal(t, "WebFetch", call.Name)
	require.Equal(t, "toolu_43", call.CallID)
	require.JSONEq(t, `{"url":"https://example.com/ref"}`, string(call.Arguments))
}

func TestSyntheticInlineImageAssetExecRepliesMatchOnlySyntheticUUIDImages(t *testing.T) {
	const syntheticPath = "/home/cursor/.cursor/projects/empty-window/assets/739a5b20-fda5-4b04-92b0-e955f40f03d9.png"
	write, err := ParseServerMessage(nativeExecMessage(40, "exec-image-write", 3,
		nativeWriteArgsPayload(syntheticPath, "", "toolu_image_write")))
	require.NoError(t, err)
	require.Len(t, SyntheticInlineImageAssetExecReplies(write.Exec), 1)
	matchedPath, matched := syntheticInlineImageAssetExecPath(write.Exec)
	require.True(t, matched)
	require.Equal(t, syntheticPath, matchedPath)

	read, err := ParseServerMessage(nativeExecMessage(41, "exec-image-read", 7,
		nativeReadArgsPayload(syntheticPath, "toolu_image_read", 0, 0)))
	require.NoError(t, err)
	require.Len(t, SyntheticInlineImageAssetExecReplies(read.Exec), 1)

	realWorkspaceWrite, err := ParseServerMessage(nativeExecMessage(42, "exec-real-write", 3,
		nativeWriteArgsPayload("D:/work/project/assets/output.png", "content", "")))
	require.NoError(t, err)
	require.Empty(t, SyntheticInlineImageAssetExecReplies(realWorkspaceWrite.Exec))

	nonUUIDWrite, err := ParseServerMessage(nativeExecMessage(43, "exec-non-uuid", 3,
		nativeWriteArgsPayload(
			"/home/cursor/.cursor/projects/empty-window/assets/output.png",
			"content",
			"",
		)))
	require.NoError(t, err)
	require.Empty(t, SyntheticInlineImageAssetExecReplies(nonUUIDWrite.Exec))
}

func TestRunAgentTurnConsumesSyntheticInlineImageAssetExecsInStream(t *testing.T) {
	const syntheticPath = "/home/cursor/.cursor/projects/empty-window/assets/739a5b20-fda5-4b04-92b0-e955f40f03d9.png"
	server := &agentTestServer{t: t, script: [][]byte{
		nativeExecMessage(44, "exec-image-write", 3,
			nativeWriteArgsPayload(syntheticPath, "", "toolu_image_write")),
		nativeExecMessage(45, "exec-image-read", 7,
			nativeReadArgsPayload(syntheticPath, "toolu_image_read", 0, 0)),
		textDeltaMessage("The screenshot shows a normal reasoning indicator."),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{
			Text:           "Inspect the attached screenshot.",
			ConversationID: "conv-inline-image-assets",
			Images: []AttachedImage{{
				Data:     []byte("image"),
				MIMEType: "image/png",
			}},
			NativeToolBridge: nativeBridgeOf(map[string]string{
				"read": "Read", "write": "Write",
			}),
		}, nil)
	require.NoError(t, err)
	require.True(t, result.TurnEnded)
	require.Equal(t, "The screenshot shows a normal reasoning indicator.", result.Text)
	require.Empty(t, result.ToolCalls)
	require.Zero(t, result.NativeToolCalls)
	require.Equal(t, 2, result.InlineImageAssetExecSuppressed)
	require.Equal(t, 2, result.ExecHandled)
	require.Zero(t, result.ExecUnanswered)
}

func TestRunAgentTurnBridgesNativeReadExec(t *testing.T) {
	shrinkToolCallGrace(t, 100*time.Millisecond)

	server := &agentTestServer{t: t, hangAfterScript: true, script: [][]byte{
		textDeltaMessage("我看看这一章"),
		nativeExecMessage(1, "exec-21", 7, nativeReadArgsPayload("章节/013.md", "toolu_21", 0, 200)),
	}}
	client, host := startAgentTestServer(t, server)

	var deltas []AgentDelta
	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{
			Text:             "读一下",
			ConversationID:   "conv-native",
			NativeToolBridge: testNativeBridge(),
		},
		func(delta AgentDelta) error {
			deltas = append(deltas, delta)
			return nil
		})
	require.NoError(t, err)

	require.True(t, result.EndedWithToolCalls())
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, 1, result.NativeToolCalls)
	require.Zero(t, result.MCPToolCalls)
	require.Equal(t, "Read", result.ToolCalls[0].Name)
	require.Equal(t, "call_toolu_21", result.ToolCalls[0].ID)
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ToolCalls[0].Arguments), &args))
	require.Equal(t, "章节/013.md", args["path"])
	require.EqualValues(t, 200, args["limit"])

	// 桥接调用也要以增量形式交给调用方。
	require.Len(t, deltas, 2)
	require.NotNil(t, deltas[1].ToolCall)

	// 不算不完整、没有 stub 计数。
	require.False(t, result.Incomplete())
	require.Zero(t, result.ExecHandled, "桥接的 exec 不该再被 stub 回执")
	require.Zero(t, result.ExecUnanswered)

	// 流上绝不能出现 exec 回执帧。
	for _, frame := range server.receivedFrames()[1:] {
		fields, err := ReadFields(frame)
		require.NoError(t, err)
		for _, field := range fields {
			require.NotEqual(t, 2, field.Number, "桥接的内置工具调用不该被 stub 回执")
		}
	}
}

func TestRunAgentTurnStillStubsUnbridgedExec(t *testing.T) {
	// 同一轮里未映射的内置工具（这里是 shell）继续走 stub，回归保护。
	server := &agentTestServer{t: t, script: [][]byte{
		nativeExecMessage(2, "exec-31", 2, EncodeStringField(1, "ls")),
		textDeltaMessage("done"),
		turnEndedMessage(),
	}}
	client, host := startAgentTestServer(t, server)

	result, err := RunAgentTurn(context.Background(), testAgentOptions(t, client, host),
		AgentTurnInput{
			Text:             "hi",
			ConversationID:   "conv-stub",
			NativeToolBridge: testNativeBridge(),
		}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, result.ExecHandled)
	require.Empty(t, result.ToolCalls)
	require.Equal(t, "done", result.Text)
}

func TestToolPolicyPreambleWithNativeAllowsBridgedBuiltins(t *testing.T) {
	preamble := ToolPolicyPreambleWithNative(
		[]McpTool{{Name: "WebSearch"}},
		nativeBridgeOf(map[string]string{"read": "Read", "grep": "Grep"}),
	)
	require.Contains(t, preamble, "You MAY use these built-in tools directly")
	require.Contains(t, preamble, "grep, read")
	require.Contains(t, preamble, McpToolNamespacePrefix+"WebSearch")
	// 未桥接的桥接键都要出现在禁用枚举里，不能只靠
	// "any other non-MCP tool" 兜底。
	require.Contains(t, preamble, "delete, diagnostics, fetch, glob, ls, shell, write, AwaitShell")
	require.NotContains(t, preamble, "unavailable here and will fail if you call it.\nThe only tools")
}

func TestToolPolicyPreambleWithNativeShellBridgedDropsFromUnavailable(t *testing.T) {
	preamble := ToolPolicyPreambleWithNative(nil, nativeBridgeOf(map[string]string{
		"read": "Read", "grep": "Grep", "ls": "LS", "glob": "Glob",
		"shell": "Bash", "write": "Write", "delete": "Delete", "fetch": "WebFetch",
		"diagnostics": "LspDiagnostics",
	}))
	require.Contains(t, preamble, "delete, diagnostics, fetch, glob, grep, ls, read, shell, write")
	// 全部桥接后，禁用枚举里不能再出现这些键。
	require.Contains(t, preamble, "(AwaitShell, StrReplace, EditNotebook")
}

func TestTranslateNativeExecBackgroundShellSpawnForcesBackground(t *testing.T) {
	// background_shell_spawn 字段布局与 shell 不同（tool_call_id 在 3），
	// 语义是强制后台执行。
	message, err := ParseServerMessage(nativeExecMessage(15, "exec-22", 16, concat(
		EncodeStringField(1, "npm run dev"),
		EncodeStringField(2, "/srv/app"),
		EncodeStringField(3, "toolu_51"),
	)))
	require.NoError(t, err)
	require.Equal(t, "background_shell_spawn", message.Exec.Kind)

	call := TranslateNativeExec(nativeBridgeOf(map[string]string{"shell": "Bash"}), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "Bash", call.Name)
	require.Equal(t, "toolu_51", call.CallID)
	require.JSONEq(t, `{
		"command": "npm run dev",
		"cwd": "/srv/app",
		"run_in_background": true
	}`, string(call.Arguments))
}

func TestTranslateNativeExecDiagnostics(t *testing.T) {
	message, err := ParseServerMessage(nativeExecMessage(16, "exec-23", 9, concat(
		EncodeStringField(1, "src/app.ts"),
		EncodeStringField(2, "toolu_52"),
	)))
	require.NoError(t, err)
	require.Equal(t, "diagnostics", message.Exec.Kind)

	call := TranslateNativeExec(nativeBridgeOf(map[string]string{"diagnostics": "LspDiagnostics"}), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "LspDiagnostics", call.Name)
	require.Equal(t, "toolu_52", call.CallID)
	require.JSONEq(t, `{"path":"src/app.ts"}`, string(call.Arguments))
}

func TestToolPolicyPreambleWithNativeOnlyBridgedTools(t *testing.T) {
	// 客户端全部工具都被桥接时没有 MCP 名单，但前言仍要输出放行说明。
	preamble := ToolPolicyPreambleWithNative(nil, nativeBridgeOf(map[string]string{"read": "Read"}))
	require.Contains(t, preamble, "read")
	require.NotContains(t, preamble, "The only other tools")
}

func TestToolPolicyPreambleUnchangedWithoutNativeBridge(t *testing.T) {
	// 不启用桥接时文案保持旧行为：内置工具全禁。
	preamble := ToolPolicyPreamble([]McpTool{{Name: "Bash"}})
	require.Contains(t, preamble, "Every built-in tool")
	require.Contains(t, preamble, "(Shell, AwaitShell, Read, Write, StrReplace, Delete, Grep, Glob, ")
	require.NotContains(t, preamble, "You MAY use these built-in tools")
}

func TestTranslateNativeExecNormalizesMultilineCallID(t *testing.T) {
	message, err := ParseServerMessage(nativeExecMessage(30, "exec-multiline", 2, concat(
		EncodeStringField(1, "pwd"),
		EncodeStringField(4, "call-abc\nfc_internal_1"),
	)))
	require.NoError(t, err)

	call := TranslateNativeExec(nativeBridgeOf(map[string]string{"shell": "Bash"}), message.Exec)
	require.NotNil(t, call)
	require.Equal(t, "call-abc", call.CallID)
}

func TestTranslateNativeExecInferredBindingOmitsUnboundOptionalArgs(t *testing.T) {
	message, err := ParseServerMessage(nativeExecMessage(31, "exec-bound-read", 7,
		nativeReadArgsPayload("a.md", "", 10, 20)))
	require.NoError(t, err)

	bridge := NativeToolBridge{"read": {
		Name: "Read",
		ArgBindings: map[string]NativeArgBinding{
			"path": {Name: "file_path"},
		},
	}}
	call := TranslateNativeExec(bridge, message.Exec)
	require.NotNil(t, call)
	require.JSONEq(t, `{"file_path":"a.md"}`, string(call.Arguments))
}

func TestTranslateNativeExecConvertsTimeoutMillisecondsToSeconds(t *testing.T) {
	message, err := ParseServerMessage(nativeExecMessage(32, "exec-timeout-seconds", 2, concat(
		EncodeStringField(1, "npm test"),
		EncodeVarintField(3, 1500),
	)))
	require.NoError(t, err)

	bridge := NativeToolBridge{"shell": {
		Name: "Bash",
		ArgBindings: map[string]NativeArgBinding{
			"command": {Name: "command"},
			"timeout": {
				Name:      "timeout_seconds",
				Transform: NativeArgTransformMillisecondsToSeconds,
			},
		},
	}}
	call := TranslateNativeExec(bridge, message.Exec)
	require.NotNil(t, call)
	require.JSONEq(t, `{"command":"npm test","timeout_seconds":2}`, string(call.Arguments))
}
