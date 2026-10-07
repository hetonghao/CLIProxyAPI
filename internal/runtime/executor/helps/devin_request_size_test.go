package helps

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func randomBase64(t *testing.T, decodedBytes int) string {
	t.Helper()
	raw := make([]byte, decodedBytes)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestWrapDevinRequestBody_CompressesOnlyLargePayloads(t *testing.T) {
	small := []byte("small payload")
	body, compressed := WrapDevinRequestBody(small)
	if compressed || body[0] != ConnectFlagData {
		t.Fatalf("small payload should stay uncompressed")
	}

	large := bytes.Repeat([]byte("history text "), devinCompressThreshold)
	body, compressed = WrapDevinRequestBody(large)
	if !compressed || body[0] != ConnectFlagCompressed {
		t.Fatalf("large payload should be gzip-compressed")
	}
	if len(body) >= len(large) {
		t.Fatalf("compressed body %d should be smaller than payload %d", len(body), len(large))
	}
	flag, decoded, err := ReadConnectFrame(bytes.NewReader(body))
	if err != nil || flag != ConnectFlagCompressed || !bytes.Equal(decoded, large) {
		t.Fatalf("compressed frame must round-trip losslessly: flag=%d err=%v", flag, err)
	}
}

func TestFitDevinImagesToRequestLimit_OmitsOldestImagesOnly(t *testing.T) {
	prompts := []DevinPrompt{
		{Source: 1, Content: "first screenshot", Images: []DevinImage{{Base64Data: randomBase64(t, 4<<20), MimeType: "image/png"}}},
		{Source: 4, ToolCallID: "call-1", Images: []DevinImage{{Base64Data: randomBase64(t, 4<<20), MimeType: "image/png"}}},
		{Source: 1, Content: "latest screenshot", Images: []DevinImage{{Base64Data: randomBase64(t, 4<<20), MimeType: "image/png"}}},
	}
	build := func(p []DevinPrompt) []byte {
		return BuildDevinGetChatMessageRequest("token", "seed", "gpt-6-astra-low", "system", p, nil, nil, 1000, "session", "cascade", nil)
	}

	protoBytes, fitted, omitted := FitDevinImagesToRequestLimit(prompts, build)
	if omitted != 1 {
		t.Fatalf("omitted = %d, want 1", omitted)
	}
	if body, _ := WrapDevinRequestBody(protoBytes); len(body) > DevinMaxRequestBodyBytes {
		t.Fatalf("fitted body %d exceeds limit", len(body))
	}
	if len(fitted[0].Images) != 0 || fitted[0].Content != "first screenshot\n"+DevinOmittedImagePlaceholder {
		t.Fatalf("oldest image should become a placeholder, got content %q images %d", fitted[0].Content, len(fitted[0].Images))
	}
	if len(fitted[1].Images) != 1 || len(fitted[2].Images) != 1 || fitted[2].Content != "latest screenshot" {
		t.Fatalf("newer images must be kept unchanged")
	}
	if len(prompts[0].Images) != 1 {
		t.Fatalf("input prompts must not be mutated")
	}
	if !strings.Contains(string(protoBytes), DevinOmittedImagePlaceholder) {
		t.Fatalf("wire payload should contain the placeholder")
	}
}

func TestFitDevinImagesToRequestLimit_NoChangeWithinLimit(t *testing.T) {
	prompts := []DevinPrompt{{Source: 1, Content: "hi", Images: []DevinImage{{Base64Data: randomBase64(t, 1<<20), MimeType: "image/png"}}}}
	build := func(p []DevinPrompt) []byte {
		return BuildDevinGetChatMessageRequest("token", "seed", "gpt-6-astra-low", "", p, nil, nil, 1000, "session", "cascade", nil)
	}
	_, fitted, omitted := FitDevinImagesToRequestLimit(prompts, build)
	if omitted != 0 || len(fitted[0].Images) != 1 {
		t.Fatalf("requests within the limit must be unchanged")
	}
}
