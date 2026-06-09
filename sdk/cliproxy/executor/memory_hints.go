package executor

import (
	"bytes"
	"strconv"
	"strings"
)

const (
	// LargeRequestBodyThresholdBytes marks JSON bodies large enough to deserve
	// retry-time cleanup even when they do not contain explicit image markers.
	LargeRequestBodyThresholdBytes = 256 * 1024

	// HeavyRequestMetadataKey marks requests that can retain meaningful memory across retries.
	HeavyRequestMetadataKey = "heavy_request"
	// RequestBodyBytesMetadataKey stores the inbound request body size.
	RequestBodyBytesMetadataKey = "request_body_bytes"
	// RequestHasImageMetadataKey records whether the inbound body appears to carry image data.
	RequestHasImageMetadataKey = "request_has_image"
)

var requestImageHintMarkers = [][]byte{
	[]byte(`"input_image"`),
	[]byte(`"image_url"`),
	[]byte(`"image_generation"`),
	[]byte(`"partial_image_b64"`),
	[]byte(`"b64_json"`),
	[]byte(`data:image`),
}

// AnnotateRequestMemoryHints records low-cost memory hints derived from the raw
// inbound body. It intentionally uses substring checks instead of full JSON
// traversal so it stays cheap for large image/base64 requests.
func AnnotateRequestMemoryHints(metadata map[string]any, body []byte) map[string]any {
	if metadata == nil {
		metadata = make(map[string]any, 3)
	}
	bodyBytes := len(body)
	hasImage := RequestBodyHasImageHint(body)
	metadata[RequestBodyBytesMetadataKey] = bodyBytes
	metadata[RequestHasImageMetadataKey] = hasImage
	metadata[HeavyRequestMetadataKey] = bodyBytes >= LargeRequestBodyThresholdBytes || hasImage
	return metadata
}

// RequestBodyHasImageHint reports whether the body contains common Responses or
// image endpoint markers for image/base64 payloads.
func RequestBodyHasImageHint(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	for _, marker := range requestImageHintMarkers {
		if bytes.Contains(body, marker) {
			return true
		}
	}
	return false
}

// IsHeavyRequest reads the memory pressure hint from executor options.
func IsHeavyRequest(opts Options) bool {
	if metadataBool(opts.Metadata, HeavyRequestMetadataKey) {
		return true
	}
	return RequestBodyBytes(opts) >= LargeRequestBodyThresholdBytes || RequestHasImage(opts)
}

// RequestHasImage returns the precomputed image hint from executor options.
func RequestHasImage(opts Options) bool {
	return metadataBool(opts.Metadata, RequestHasImageMetadataKey)
}

// RequestBodyBytes returns the precomputed inbound request body size.
func RequestBodyBytes(opts Options) int {
	return metadataInt(opts.Metadata, RequestBodyBytesMetadataKey)
}

func metadataBool(metadata map[string]any, key string) bool {
	if len(metadata) == 0 {
		return false
	}
	switch v := metadata[key].(type) {
	case bool:
		return v
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(v))
		return parsed
	default:
		return false
	}
}

func metadataInt(metadata map[string]any, key string) int {
	if len(metadata) == 0 {
		return 0
	}
	switch v := metadata[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(v))
		return parsed
	default:
		return 0
	}
}
