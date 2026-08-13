package cursor

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const attachedImageMarker = "[Attached image]"

// MaxImageBytes limits one decoded inline image before it is copied into the
// Cursor Agent protobuf request.
var MaxImageBytes = envBytes("CURSOR_MAX_IMAGE_BYTES", 20*1024*1024)

// AttachedImage is placed in SelectedContext.selected_images.
// ID is optional in normal requests and lets byte-level tests be deterministic.
type AttachedImage struct {
	ID       string
	Data     []byte
	MIMEType string
}

func parseImageDataURI(raw string) (AttachedImage, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return AttachedImage{}, fmt.Errorf(
			"cursor image input currently requires a base64 data URL; remote image URLs are not fetched",
		)
	}

	comma := strings.IndexByte(value, ',')
	if comma < 0 {
		return AttachedImage{}, fmt.Errorf("invalid image data URL: missing comma")
	}
	metadata := value[len("data:"):comma]
	payload := value[comma+1:]

	parts := strings.Split(metadata, ";")
	mimeType := strings.ToLower(strings.TrimSpace(parts[0]))
	if !strings.HasPrefix(mimeType, "image/") {
		return AttachedImage{}, fmt.Errorf("invalid image data URL MIME type %q", mimeType)
	}

	base64Encoded := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			base64Encoded = true
			break
		}
	}
	if !base64Encoded {
		return AttachedImage{}, fmt.Errorf("cursor image input requires base64-encoded image data")
	}

	if MaxImageBytes > 0 && base64.StdEncoding.DecodedLen(len(payload)) > MaxImageBytes {
		return AttachedImage{}, fmt.Errorf("image exceeds the %d byte limit", MaxImageBytes)
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return AttachedImage{}, fmt.Errorf("invalid base64 image data: %w", err)
	}
	if len(data) == 0 {
		return AttachedImage{}, fmt.Errorf("image data is empty")
	}
	if MaxImageBytes > 0 && len(data) > MaxImageBytes {
		return AttachedImage{}, fmt.Errorf("image exceeds the %d byte limit", MaxImageBytes)
	}
	return AttachedImage{Data: data, MIMEType: mimeType}, nil
}
