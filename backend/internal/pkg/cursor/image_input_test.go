package cursor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIConversationExtractsInlineImage(t *testing.T) {
	req := parseOpenAIRequest(t, `{
		"messages":[{"role":"user","content":[
			{"type":"text","text":"What is in this image?"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}
		]}]
	}`)

	conversation := req.Conversation()
	require.NoError(t, conversation.ValidationError())
	require.Len(t, conversation.Images(), 1)
	require.Equal(t, []byte("hello"), conversation.Images()[0].Data)
	require.Equal(t, "image/png", conversation.Images()[0].MIMEType)
	require.Equal(t, "What is in this image?\n[Attached image]", conversation.Render())
}

func TestOpenAIConversationRejectsRemoteImageURL(t *testing.T) {
	req := parseOpenAIRequest(t, `{
		"messages":[{"role":"user","content":[
			{"type":"text","text":"describe it"},
			{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}
		]}]
	}`)

	conversation := req.Conversation()
	require.Error(t, conversation.ValidationError())
	require.Contains(t, conversation.ValidationError().Error(), "remote image URLs are not fetched")
	require.Empty(t, conversation.Images())
}

func TestAnthropicConversationExtractsBase64Image(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"messages":[{"role":"user","content":[
			{"type":"text","text":"describe it"},
			{"type":"image","source":{
				"type":"base64","media_type":"image/jpeg","data":"aGVsbG8="
			}}
		]}]
	}`)

	conversation := req.Conversation()
	require.NoError(t, conversation.ValidationError())
	require.Len(t, conversation.Images(), 1)
	require.Equal(t, []byte("hello"), conversation.Images()[0].Data)
	require.Equal(t, "image/jpeg", conversation.Images()[0].MIMEType)
	require.Equal(t, "describe it\n[Attached image]", conversation.Render())
}

func TestEncodeRunRequestCarriesSelectedInlineImages(t *testing.T) {
	body, err := EncodeRunRequest(RunRequestInput{
		Text:           "describe it\n[Attached image]",
		ConversationID: "conv-image",
		MessageID:      "msg-image",
		ModelID:        "default",
		Images: []AttachedImage{{
			ID:       "image-1",
			Data:     []byte("hello"),
			MIMEType: "image/png",
		}},
	})
	require.NoError(t, err)

	outer, err := ReadFields(body)
	require.NoError(t, err)
	runRequest, ok := FieldBytes(outer, 1)
	require.True(t, ok)
	runRequestFields, err := ReadFields(runRequest)
	require.NoError(t, err)
	actionEnvelope, ok := FieldBytes(runRequestFields, 2)
	require.True(t, ok)
	actionEnvelopeFields, err := ReadFields(actionEnvelope)
	require.NoError(t, err)
	action, ok := FieldBytes(actionEnvelopeFields, 1)
	require.True(t, ok)
	actionFields, err := ReadFields(action)
	require.NoError(t, err)
	userMessage, ok := FieldBytes(actionFields, 1)
	require.True(t, ok)
	userMessageFields, err := ReadFields(userMessage)
	require.NoError(t, err)
	selectedContext, ok := FieldBytes(userMessageFields, 3)
	require.True(t, ok)
	selectedContextFields, err := ReadFields(selectedContext)
	require.NoError(t, err)
	selectedImage, ok := FieldBytes(selectedContextFields, 1)
	require.True(t, ok)
	selectedImageFields, err := ReadFields(selectedImage)
	require.NoError(t, err)

	require.Equal(t, []byte("hello"), mustFieldBytes(t, selectedImageFields, 8))
	require.Equal(t, "image-1", FieldString(selectedImageFields, 2))
	require.Equal(t, "image/png", FieldString(selectedImageFields, 7))
}

func mustFieldBytes(t *testing.T, fields []Field, number int) []byte {
	t.Helper()
	value, ok := FieldBytes(fields, number)
	require.True(t, ok)
	return value
}

func TestOpenAIMessageTextKeepsLegacyTextOnlyBehavior(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"line one"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}},{"type":"text","text":"line two"}]`)
	require.Equal(t, "line one\nline two", messageText(raw))
}
