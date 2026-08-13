//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCursorGatewayRejectsInvalidEffortAcrossProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &CursorGatewayService{}
	account := &Account{ID: 46, Platform: PlatformCursor}

	tests := []struct {
		name string
		body string
		want string
		call func(context.Context, *gin.Context, *Account, []byte) (*ForwardResult, error)
	}{
		{
			name: "chat_completions",
			body: `{"model":"cursor/grok-4.6","reasoning_effort":" ","messages":[{"role":"user","content":"hi"}]}`,
			want: "effort must not be empty",
			call: svc.forwardChatCompletionsOnce,
		},
		{
			name: "responses",
			body: `{"model":"cursor/grok-4.6","reasoning":{"effort":" "},"input":"hi"}`,
			want: "effort must not be empty",
			call: svc.forwardResponsesOnce,
		},
		{
			name: "anthropic_messages",
			body: `{"model":"cursor/grok-4.6","max_tokens":16,"output_config":{"effort":" "},"messages":[{"role":"user","content":"hi"}]}`,
			want: "effort must not be empty",
			call: svc.forwardMessagesOnce,
		},
		{
			name: "chat_rejects_max_even_when_overridden",
			body: `{"model":"cursor/grok-4.6","reasoning_effort":"max","cursor_options":{"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`,
			want: `effort "max" is not supported`,
			call: svc.forwardChatCompletionsOnce,
		},
		{
			name: "responses_rejects_extra_high_even_when_overridden",
			body: `{"model":"cursor/grok-4.6","reasoning":{"effort":"extra-high"},"cursor_options":{"effort":"high"},"input":"hi"}`,
			want: `effort "extra-high" is not supported`,
			call: svc.forwardResponsesOnce,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

			result, err := tt.call(context.Background(), c, account, []byte(tt.body))

			require.Nil(t, result)
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			var response struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.Contains(t, response.Error.Message, tt.want)
		})
	}
}
