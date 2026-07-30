#!/bin/bash
# 三个客户端协议的端到端验收：opencode(chat/completions) / Claude Code(messages) / Codex(responses)。
# 在服务器本机跑，API Key 直接从库里取，不出机器。
set -uo pipefail

B=http://127.0.0.1:8080
PSQL="docker exec sub2api-postgres psql -U sub2api -d sub2api -t -A"
KEY=$($PSQL -c "select key from api_keys where deleted_at is null and status='active' order by id limit 1;" | tr -d '[:space:]')
if [ -z "$KEY" ]; then
  echo "FATAL: 没有可用的 API Key"
  exit 1
fi
echo "using api key: ${KEY:0:8}…(${#KEY} chars)"

PASS=0
FAIL=0
check() {
  local name="$1" cond="$2" detail="${3:-}"
  if [ "$cond" = "1" ]; then
    echo "  [PASS] $name"
    PASS=$((PASS+1))
  else
    echo "  [FAIL] $name ${detail}"
    FAIL=$((FAIL+1))
  fi
}

TOOLS_OPENAI='[{"type":"function","function":{"name":"Bash","description":"Execute a shell command and return stdout.","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}}]'
PROMPT='请用 Bash 工具执行 echo ringstar-e2e-marker，然后把真实输出原样告诉我。'

echo
echo "=============== 1. 纯对话回归（无工具，不能被破坏）==============="
RESP=$(curl -s -m 120 "$B/v1/chat/completions" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"model":"cursor/default","stream":false,"messages":[{"role":"user","content":"用一句话回答：1+1 等于几？"}]}')
echo "$RESP" | head -c 600; echo
CONTENT=$(echo "$RESP" | jq -r '.choices[0].message.content // ""')
FINISH=$(echo "$RESP" | jq -r '.choices[0].finish_reason // ""')
check "返回了正文" "$([ -n "$CONTENT" ] && echo 1 || echo 0)"
check "finish_reason=stop" "$([ "$FINISH" = "stop" ] && echo 1 || echo 0)" "got=$FINISH"

echo
echo "=============== 2. opencode 链路：/v1/chat/completions + tools ==============="
REQ=$(jq -n --arg p "$PROMPT" --argjson t "$TOOLS_OPENAI" \
  '{model:"cursor/default",stream:false,tools:$t,messages:[{role:"user",content:$p}]}')
RESP=$(curl -s -m 180 "$B/v1/chat/completions" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d "$REQ")
echo "$RESP" | head -c 900; echo
FINISH=$(echo "$RESP" | jq -r '.choices[0].finish_reason // ""')
TOOLNAME=$(echo "$RESP" | jq -r '.choices[0].message.tool_calls[0].function.name // ""')
TOOLARGS=$(echo "$RESP" | jq -r '.choices[0].message.tool_calls[0].function.arguments // ""')
CALLID=$(echo "$RESP" | jq -r '.choices[0].message.tool_calls[0].id // ""')
check "finish_reason=tool_calls" "$([ "$FINISH" = "tool_calls" ] && echo 1 || echo 0)" "got=$FINISH"
check "工具名=Bash" "$([ "$TOOLNAME" = "Bash" ] && echo 1 || echo 0)" "got=$TOOLNAME"
check "入参是合法 JSON 且含 command" "$(echo "$TOOLARGS" | jq -e '.command' >/dev/null 2>&1 && echo 1 || echo 0)" "got=$TOOLARGS"
check "call id 形如 call_*" "$(case "$CALLID" in call_*) echo 1;; *) echo 0;; esac)" "got=$CALLID"

echo
echo "--------------- 2b. 带工具结果重放，模型应据此作答 ---------------"
REQ=$(jq -n --arg p "$PROMPT" --argjson t "$TOOLS_OPENAI" --arg id "$CALLID" --arg args "$TOOLARGS" \
  '{model:"cursor/default",stream:false,tools:$t,messages:[
     {role:"user",content:$p},
     {role:"assistant",content:null,tool_calls:[{id:$id,type:"function",function:{name:"Bash",arguments:$args}}]},
     {role:"tool",tool_call_id:$id,name:"Bash",content:"ringstar-e2e-marker\n"}
   ]}')
RESP=$(curl -s -m 180 "$B/v1/chat/completions" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d "$REQ")
echo "$RESP" | head -c 900; echo
CONTENT=$(echo "$RESP" | jq -r '.choices[0].message.content // ""')
FINISH=$(echo "$RESP" | jq -r '.choices[0].finish_reason // ""')
check "第二轮以 stop 收尾（没再要工具）" "$([ "$FINISH" = "stop" ] && echo 1 || echo 0)" "got=$FINISH"
check "回答里出现工具结果" "$(echo "$CONTENT" | grep -q 'ringstar-e2e-marker' && echo 1 || echo 0)" "got=$CONTENT"

echo
echo "--------------- 2c. 流式：tool_calls 增量 + finish_reason ---------------"
REQ=$(jq -n --arg p "$PROMPT" --argjson t "$TOOLS_OPENAI" \
  '{model:"cursor/default",stream:true,tools:$t,messages:[{role:"user",content:$p}]}')
STREAM=$(curl -s -m 180 -N "$B/v1/chat/completions" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d "$REQ")
echo "$STREAM" | grep -o '"tool_calls":\[[^]]*\]' | head -c 400; echo
check "流里出现 tool_calls 增量" "$(echo "$STREAM" | grep -q '"tool_calls"' && echo 1 || echo 0)"
check "流里 finish_reason=tool_calls" "$(echo "$STREAM" | grep -q '"finish_reason":"tool_calls"' && echo 1 || echo 0)"
check "流以 [DONE] 收尾" "$(echo "$STREAM" | grep -q 'data: \[DONE\]' && echo 1 || echo 0)"

echo
echo "=============== 3. Claude Code 链路：/v1/messages + tools ==============="
TOOLS_ANTHROPIC='[{"name":"Bash","description":"Execute a shell command and return stdout.","input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]'
REQ=$(jq -n --arg p "$PROMPT" --argjson t "$TOOLS_ANTHROPIC" \
  '{model:"cursor/default",max_tokens:4096,stream:false,tools:$t,messages:[{role:"user",content:$p}]}')
RESP=$(curl -s -m 180 "$B/v1/messages" -H "x-api-key: $KEY" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -d "$REQ")
echo "$RESP" | head -c 900; echo
STOP=$(echo "$RESP" | jq -r '.stop_reason // ""')
TU_NAME=$(echo "$RESP" | jq -r '[.content[] | select(.type=="tool_use")][0].name // ""')
TU_ID=$(echo "$RESP" | jq -r '[.content[] | select(.type=="tool_use")][0].id // ""')
TU_INPUT=$(echo "$RESP" | jq -c '[.content[] | select(.type=="tool_use")][0].input // {}')
check "stop_reason=tool_use" "$([ "$STOP" = "tool_use" ] && echo 1 || echo 0)" "got=$STOP"
check "tool_use 工具名=Bash" "$([ "$TU_NAME" = "Bash" ] && echo 1 || echo 0)" "got=$TU_NAME"
check "tool_use id 形如 toolu_*" "$(case "$TU_ID" in toolu_*) echo 1;; *) echo 0;; esac)" "got=$TU_ID"
check "input 含 command" "$(echo "$TU_INPUT" | jq -e '.command' >/dev/null 2>&1 && echo 1 || echo 0)" "got=$TU_INPUT"

echo
echo "--------------- 3b. Claude Code 带 tool_result 重放 ---------------"
REQ=$(jq -n --arg p "$PROMPT" --argjson t "$TOOLS_ANTHROPIC" --arg id "$TU_ID" --argjson inp "$TU_INPUT" \
  '{model:"cursor/default",max_tokens:4096,stream:false,tools:$t,messages:[
     {role:"user",content:$p},
     {role:"assistant",content:[{type:"tool_use",id:$id,name:"Bash",input:$inp}]},
     {role:"user",content:[{type:"tool_result",tool_use_id:$id,content:"ringstar-e2e-marker\n"}]}
   ]}')
RESP=$(curl -s -m 180 "$B/v1/messages" -H "x-api-key: $KEY" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -d "$REQ")
echo "$RESP" | head -c 900; echo
STOP=$(echo "$RESP" | jq -r '.stop_reason // ""')
TEXT=$(echo "$RESP" | jq -r '[.content[] | select(.type=="text")][0].text // ""')
check "第二轮 stop_reason=end_turn" "$([ "$STOP" = "end_turn" ] && echo 1 || echo 0)" "got=$STOP"
check "回答里出现工具结果" "$(echo "$TEXT" | grep -q 'ringstar-e2e-marker' && echo 1 || echo 0)" "got=$TEXT"

echo
echo "--------------- 3c. Claude Code 流式 SSE 事件序列 ---------------"
REQ=$(jq -n --arg p "$PROMPT" --argjson t "$TOOLS_ANTHROPIC" \
  '{model:"cursor/default",max_tokens:4096,stream:true,tools:$t,messages:[{role:"user",content:$p}]}')
STREAM=$(curl -s -m 180 -N "$B/v1/messages" -H "x-api-key: $KEY" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -d "$REQ")
echo "$STREAM" | grep '^event:' | sort | uniq -c
check "有 message_start" "$(echo "$STREAM" | grep -q 'event: message_start' && echo 1 || echo 0)"
check "有 tool_use 块" "$(echo "$STREAM" | grep -q '"type":"tool_use"' && echo 1 || echo 0)"
check "有 input_json_delta" "$(echo "$STREAM" | grep -q 'input_json_delta' && echo 1 || echo 0)"
check "stop_reason=tool_use" "$(echo "$STREAM" | grep -q '"stop_reason":"tool_use"' && echo 1 || echo 0)"
check "有 message_stop" "$(echo "$STREAM" | grep -q 'event: message_stop' && echo 1 || echo 0)"
OPENS=$(echo "$STREAM" | grep -c 'event: content_block_start')
CLOSES=$(echo "$STREAM" | grep -c 'event: content_block_stop')
check "content_block 开合成对 ($OPENS/$CLOSES)" "$([ "$OPENS" = "$CLOSES" ] && echo 1 || echo 0)"

echo
echo "--------------- 3d. count_tokens 不再 404 ---------------"
CODE=$(curl -s -o /tmp/ct.json -w '%{http_code}' -m 60 "$B/v1/messages/count_tokens" -H "x-api-key: $KEY" \
  -H 'Content-Type: application/json' -d '{"model":"cursor/default","messages":[{"role":"user","content":"hi"}]}')
cat /tmp/ct.json; echo
check "count_tokens 返回 200" "$([ "$CODE" = "200" ] && echo 1 || echo 0)" "got=$CODE"

echo
echo "=============== 4. Codex 链路：/v1/responses + tools ==============="
TOOLS_RESPONSES='[{"type":"function","name":"Bash","description":"Execute a shell command and return stdout.","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]},"strict":false}]'
REQ=$(jq -n --arg p "$PROMPT" --argjson t "$TOOLS_RESPONSES" \
  '{model:"cursor/default",stream:false,store:false,tools:$t,input:[{type:"message",role:"user",content:[{type:"input_text",text:$p}]}]}')
RESP=$(curl -s -m 180 "$B/v1/responses" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d "$REQ")
echo "$RESP" | head -c 1200; echo
FC_NAME=$(echo "$RESP" | jq -r '[.output[]? | select(.type=="function_call")][0].name // ""')
FC_ARGS=$(echo "$RESP" | jq -r '[.output[]? | select(.type=="function_call")][0].arguments // ""')
check "output 里有 function_call" "$([ -n "$FC_NAME" ] && echo 1 || echo 0)" "got=$FC_NAME"
check "function_call 名=Bash" "$([ "$FC_NAME" = "Bash" ] && echo 1 || echo 0)" "got=$FC_NAME"
check "arguments 含 command" "$(echo "$FC_ARGS" | jq -e '.command' >/dev/null 2>&1 && echo 1 || echo 0)" "got=$FC_ARGS"

echo
echo "--------------- 4b. Codex 流式事件 ---------------"
REQ=$(jq -n --arg p "$PROMPT" --argjson t "$TOOLS_RESPONSES" \
  '{model:"cursor/default",stream:true,store:false,tools:$t,input:[{type:"message",role:"user",content:[{type:"input_text",text:$p}]}]}')
STREAM=$(curl -s -m 180 -N "$B/v1/responses" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d "$REQ")
echo "$STREAM" | grep '^event:' | sort | uniq -c
check "有 response.created" "$(echo "$STREAM" | grep -q 'response.created' && echo 1 || echo 0)"
check "有 function_call_arguments" "$(echo "$STREAM" | grep -q 'function_call_arguments' && echo 1 || echo 0)"
check "有 response.completed" "$(echo "$STREAM" | grep -q 'response.completed' && echo 1 || echo 0)"

echo
echo "--------------- 4c. Codex 带 function_call_output 重放 ---------------"
REQ=$(jq -n --arg p "$PROMPT" --argjson t "$TOOLS_RESPONSES" --arg args "$FC_ARGS" \
  '{model:"cursor/default",stream:false,store:false,tools:$t,input:[
     {type:"message",role:"user",content:[{type:"input_text",text:$p}]},
     {type:"function_call",call_id:"call_e2e_1",name:"Bash",arguments:$args},
     {type:"function_call_output",call_id:"call_e2e_1",output:"ringstar-e2e-marker\n"}
   ]}')
RESP=$(curl -s -m 180 "$B/v1/responses" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d "$REQ")
echo "$RESP" | head -c 1200; echo
TEXT=$(echo "$RESP" | jq -r '[.output[]? | select(.type=="message") | .content[]? | select(.type=="output_text") | .text][0] // ""')
check "回答里出现工具结果" "$(echo "$TEXT" | grep -q 'ringstar-e2e-marker' && echo 1 || echo 0)" "got=$TEXT"

echo
echo "==================================================="
echo "通过 $PASS 项，失败 $FAIL 项"
[ "$FAIL" = "0" ] && echo "E2E_ALL_PASS" || echo "E2E_HAS_FAILURE"
