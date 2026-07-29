// 从反代 proto.js / cursor-agent-env.js / agent-client.js 原样摘出的编码器，
// 用来给 Go 侧生成逐字节对拍向量。不参与构建：
//   node proto_reference.js

function encodeVarint(value) {
  const out = [];
  let v = Number(value);
  if (!Number.isFinite(v) || v < 0) v = 0;
  v = Math.floor(v);
  while (v >= 0x80) {
    out.push((v & 0x7f) | 0x80);
    v = Math.floor(v / 128);
  }
  out.push(v & 0x7f);
  return Buffer.from(out);
}

function encodeTag(fieldNum, wireType) {
  return encodeVarint((fieldNum << 3) | wireType);
}

function encodeString(fieldNum, str) {
  const data = Buffer.from(String(str), "utf8");
  return Buffer.concat([encodeTag(fieldNum, 2), encodeVarint(data.length), data]);
}

function encodeBytes(fieldNum, bytes) {
  const data = Buffer.isBuffer(bytes) ? bytes : Buffer.from(bytes);
  return Buffer.concat([encodeTag(fieldNum, 2), encodeVarint(data.length), data]);
}

function encodeInt(fieldNum, value) {
  return Buffer.concat([encodeTag(fieldNum, 0), encodeVarint(value)]);
}

function encodeBool(fieldNum, value) {
  return encodeInt(fieldNum, value ? 1 : 0);
}

function encodeModelParam(id, value) {
  return encodeBytes(3, Buffer.concat([encodeString(1, id), encodeString(2, String(value))]));
}

function encodeRequestedModel({ modelId, params = [], maxMode }) {
  const parts = [encodeString(1, modelId)];
  if (maxMode === true) parts.push(encodeBool(2, true));
  else if (maxMode === false) parts.push(encodeBool(2, false));
  for (const p of params) parts.push(encodeModelParam(p[0], p[1]));
  return Buffer.concat(parts);
}

const OFFICIAL_SELECTED_SUBAGENT_MODELS = [
  { modelId: "default", params: [] },
  { modelId: "grok-4.5", params: [["effort", "high"], ["fast", "true"]] },
  { modelId: "composer-2.5", params: [["fast", "true"]] },
  {
    modelId: "claude-opus-4-8",
    params: [["thinking", "true"], ["context", "300k"], ["effort", "high"], ["fast", "false"]]
  }
];

function encodeSelectedSubagentModels(models = OFFICIAL_SELECTED_SUBAGENT_MODELS) {
  return Buffer.concat(models.map((m) => encodeBytes(14, encodeRequestedModel(m))));
}

function encodeRunRequest({
  text,
  messageId,
  conversationId,
  requestContextBin,
  modelId,
  modelParams,
  maxMode,
  conversationState
}) {
  const rich = JSON.stringify({
    type: "doc",
    content: [{ type: "paragraph", content: [{ type: "text", text }] }]
  });
  const userMessage = Buffer.concat([
    encodeString(1, text),
    encodeString(2, messageId),
    encodeBytes(3, Buffer.alloc(0)),
    encodeInt(4, 1),
    encodeString(8, rich)
  ]);
  const userMessageAction = Buffer.concat([
    encodeBytes(1, userMessage),
    encodeBytes(2, requestContextBin)
  ]);
  const action = encodeBytes(1, userMessageAction);
  const requestedModel = encodeRequestedModel({ modelId, maxMode, params: modelParams });
  const stateBytes =
    conversationState && conversationState.length ? conversationState : Buffer.alloc(0);
  const runRequest = Buffer.concat([
    encodeBytes(1, stateBytes),
    encodeBytes(2, action),
    encodeBytes(4, Buffer.alloc(0)),
    encodeString(5, conversationId),
    encodeBytes(9, requestedModel),
    encodeBool(10, false),
    encodeSelectedSubagentModels(),
    encodeBool(19, true),
    encodeBool(21, false),
    encodeBool(22, false),
    encodeBool(23, true)
  ]);
  return encodeBytes(1, runRequest);
}

// env-only RequestContext，参数与 Go 侧 DefaultRequestContextEnv 对齐。
function buildRequestContextEnv() {
  const home = "/home/cursor";
  const projectFolder = `${home}/.cursor/projects/empty-window`;
  return Buffer.concat([
    encodeString(1, "linux 6.8.0"),
    encodeString(2, home),
    encodeString(3, "bash"),
    encodeString(7, `${projectFolder}/terminals`),
    encodeString(10, "UTC"),
    encodeString(11, projectFolder),
    encodeString(12, `${projectFolder}/agent-transcripts`),
    encodeBool(14, false),
    encodeBool(16, true),
    encodeBool(19, false),
    encodeBool(20, false),
    encodeBool(22, false)
  ]);
}

function buildRequestContext() {
  return Buffer.concat([
    encodeBytes(4, buildRequestContextEnv()),
    encodeBool(17, true),
    encodeBool(24, true),
    encodeString(26, "enabled"),
    encodeString(27, "enabled"),
    encodeBytes(28, Buffer.alloc(0)),
    encodeBool(32, true),
    encodeBool(33, true),
    encodeBool(35, false),
    encodeBool(36, true),
    encodeBool(39, true),
    encodeBool(40, true),
    encodeBool(41, true),
    encodeBool(42, true),
    encodeBool(43, false),
    encodeBool(44, true),
    encodeBool(45, false),
    encodeBool(50, false)
  ]);
}

function encodeEnvelope(payload, flags = 0) {
  const body = Buffer.isBuffer(payload) ? payload : Buffer.from(payload);
  const header = Buffer.alloc(5);
  header[0] = flags;
  header.writeUInt32BE(body.length, 1);
  return Buffer.concat([header, body]);
}

function encodeExecClientMessage({ id, execId, resultFieldNum, resultBytes }) {
  const parts = [encodeInt(1, id == null ? 0 : id)];
  if (execId) parts.push(encodeString(15, execId));
  parts.push(encodeBytes(resultFieldNum, resultBytes));
  return encodeBytes(2, Buffer.concat(parts));
}

const hex = (b) => Buffer.from(b).toString("hex");
const ctx = buildRequestContext();

const out = {
  requestContextEnv: hex(buildRequestContextEnv()),
  requestContext: hex(ctx),
  requestedModelPlain: hex(encodeRequestedModel({ modelId: "default", params: [] })),
  requestedModelWithParams: hex(
    encodeRequestedModel({ modelId: "grok-4.5", params: [["effort", "high"], ["fast", "true"]] })
  ),
  requestedModelMaxModeFalse: hex(
    encodeRequestedModel({ modelId: "gpt-5.6-sol", params: [], maxMode: false })
  ),
  selectedSubagentModels: hex(encodeSelectedSubagentModels()),
  heartbeat: hex(encodeBytes(7, Buffer.alloc(0))),
  envelopeEmpty: hex(encodeEnvelope(Buffer.alloc(0), 0)),
  envelopeEndStream: hex(encodeEnvelope(Buffer.from('{"error":{"code":"x"}}'), 2)),
  execClientMessage: hex(
    encodeExecClientMessage({ id: 7, execId: "e-1", resultFieldNum: 2, resultBytes: Buffer.from([1, 2, 3]) })
  ),
  execClientMessageZeroID: hex(
    encodeExecClientMessage({ id: 0, execId: "", resultFieldNum: 8, resultBytes: Buffer.alloc(0) })
  ),
  runRequest: hex(
    encodeRunRequest({
      text: "hello agent",
      messageId: "11111111-2222-3333-4444-555555555555",
      conversationId: "conv-fixed-1",
      requestContextBin: ctx,
      modelId: "claude-opus-4-8",
      modelParams: [["effort", "high"], ["fast", "true"]],
      conversationState: Buffer.alloc(0)
    })
  ),
  tiptapSpecialChars: hex(
    encodeRunRequest({
      text: 'if (a < b && c > d) { return "<tag>"; }',
      messageId: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      conversationId: "conv-fixed-3",
      requestContextBin: ctx,
      modelId: "default",
      modelParams: [],
      conversationState: Buffer.alloc(0)
    })
  ),
  runRequestWithState: hex(
    encodeRunRequest({
      text: "second turn",
      messageId: "66666666-7777-8888-9999-000000000000",
      conversationId: "conv-fixed-2",
      requestContextBin: ctx,
      modelId: "default",
      modelParams: [],
      conversationState: Buffer.from([0xde, 0xad, 0xbe, 0xef])
    })
  )
};

console.log(JSON.stringify(out, null, 2));
