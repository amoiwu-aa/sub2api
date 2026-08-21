package cursor

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
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
	require.True(t, strings.HasSuffix(
		conversation.Render(),
		"What is in this image?\n[Attached image]",
	))
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
	require.True(t, strings.HasSuffix(
		conversation.Render(),
		"describe it\n[Attached image]",
	))
}

func TestCurrentInputImagesExcludesHistoryAndDeduplicates(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"first image"},
				{"type":"image","source":{
					"type":"base64","media_type":"image/png","data":"aGlzdG9yeQ=="
				}}
			]},
			{"role":"assistant","content":"seen"},
			{"role":"user","content":[
				{"type":"text","text":"current image"},
				{"type":"image","source":{
					"type":"base64","media_type":"image/png","data":"Y3VycmVudA=="
				}},
				{"type":"image","source":{
					"type":"base64","media_type":"image/png","data":"Y3VycmVudA=="
				}}
			]}
		]
	}`)

	conversation := req.Conversation()
	require.NoError(t, conversation.ValidationError())
	require.Len(t, conversation.Images(), 3)
	current := conversation.CurrentInputImages()
	require.Len(t, current, 1)
	require.Equal(t, []byte("current"), current[0].Data)
	require.Contains(t, conversation.Render(), "<image_input_policy>")
	require.Contains(t, conversation.Render(), "Inspect them directly")
}

func TestCurrentInputImagesOmitsHistoricalImageWhenLatestTurnIsText(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"first image"},
				{"type":"image","source":{
					"type":"base64","media_type":"image/png","data":"aGlzdG9yeQ=="
				}}
			]},
			{"role":"assistant","content":"seen"},
			{"role":"user","content":"continue without an image"}
		]
	}`)

	conversation := req.Conversation()
	require.NoError(t, conversation.ValidationError())
	require.Len(t, conversation.Images(), 1)
	require.Empty(t, conversation.CurrentInputImages())
	require.NotContains(t, conversation.Render(), "<image_input_policy>")
}

func TestCurrentInputImagesIncludesTrailingToolResultImage(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"messages":[
			{"role":"user","content":"capture a screenshot"},
			{"role":"assistant","content":[{
				"type":"tool_use","id":"toolu_image","name":"capture","input":{}
			}]},
			{"role":"user","content":[{
				"type":"tool_result","tool_use_id":"toolu_image","content":[{
					"type":"image","source":{
						"type":"base64","media_type":"image/png","data":"dG9vbC1pbWFnZQ=="
					}
				}]
			}]}
		]
	}`)

	conversation := req.Conversation()
	require.NoError(t, conversation.ValidationError())
	current := conversation.CurrentInputImages()
	require.Len(t, current, 1)
	require.Equal(t, []byte("tool-image"), current[0].Data)
}

func TestCurrentInputImagesRetainsInstructionImageAcrossToolContinuation(t *testing.T) {
	req := parseAnthropicRequest(t, `{
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"inspect this image and read the config"},
				{"type":"image","source":{
					"type":"base64","media_type":"image/png","data":"dGFzay1pbWFnZQ=="
				}}
			]},
			{"role":"assistant","content":[{
				"type":"tool_use","id":"toolu_read","name":"Read","input":{"file_path":"config.json"}
			}]},
			{"role":"user","content":[{
				"type":"tool_result","tool_use_id":"toolu_read","content":"{}"
			}]}
		]
	}`)

	conversation := req.Conversation()
	require.NoError(t, conversation.ValidationError())
	current := conversation.CurrentInputImages()
	require.Len(t, current, 1)
	require.Equal(t, []byte("task-image"), current[0].Data)
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
			Width:    1280,
			Height:   720,
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

	blobWithData := mustFieldBytes(t, selectedImageFields, 9)
	blobWithDataFields, err := ReadFields(blobWithData)
	require.NoError(t, err)
	require.Equal(t,
		[]byte{0x2c, 0xf2, 0x4d, 0xba, 0x5f, 0xb0, 0xa3, 0x0e, 0x26, 0xe8, 0x3b, 0x2a, 0xc5, 0xb9, 0xe2, 0x9e, 0x1b, 0x16, 0x1e, 0x5c, 0x1f, 0xa7, 0x42, 0x5e, 0x73, 0x04, 0x33, 0x62, 0x93, 0x8b, 0x98, 0x24},
		mustFieldBytes(t, blobWithDataFields, 1),
	)
	require.Equal(t, []byte("hello"), mustFieldBytes(t, blobWithDataFields, 2))
	require.Equal(t, "image-1", FieldString(selectedImageFields, 2))
	require.Equal(t, "image/png", FieldString(selectedImageFields, 7))

	dimension := mustFieldBytes(t, selectedImageFields, 4)
	dimensionFields, err := ReadFields(dimension)
	require.NoError(t, err)
	require.Equal(t, uint64(1280), mustFieldVarint(t, dimensionFields, 1))
	require.Equal(t, uint64(720), mustFieldVarint(t, dimensionFields, 2))
	_, hasLegacyData := FieldBytes(selectedImageFields, 8)
	require.False(t, hasLegacyData)
}

func mustFieldBytes(t *testing.T, fields []Field, number int) []byte {
	t.Helper()
	value, ok := FieldBytes(fields, number)
	require.True(t, ok)
	return value
}

func mustFieldVarint(t *testing.T, fields []Field, number int) uint64 {
	t.Helper()
	for _, field := range fields {
		if field.Number == number && field.WireType == wireVarint {
			return field.Varint
		}
	}
	require.FailNow(t, "missing varint field", "field %d", number)
	return 0
}

func TestParseImageDataURIDetectsActualMetadata(t *testing.T) {
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 7, 5))))

	parsed, err := parseImageDataURI(
		"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes()),
	)
	require.NoError(t, err)
	require.Equal(t, "image/png", parsed.MIMEType)
	require.Equal(t, 7, parsed.Width)
	require.Equal(t, 5, parsed.Height)
}

func TestInlineImageRequestContextOmitsSyntheticFilesystemPaths(t *testing.T) {
	fields, err := ReadFields(EncodeRequestContextEnv(InlineImageRequestContextEnv("empty-window")))
	require.NoError(t, err)

	for _, fieldNumber := range []int{2, 7, 11, 12} {
		_, present := FieldBytes(fields, fieldNumber)
		require.False(t, present, "field %d must be omitted", fieldNumber)
	}
	require.Equal(t, "linux 6.8.0", FieldString(fields, 1))
	require.Equal(t, "bash", FieldString(fields, 3))
	require.Equal(t, "UTC", FieldString(fields, 10))
}

func TestOpenAIMessageTextKeepsLegacyTextOnlyBehavior(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"line one"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}},{"type":"text","text":"line two"}]`)
	require.Equal(t, "line one\nline two", messageText(raw))
}
