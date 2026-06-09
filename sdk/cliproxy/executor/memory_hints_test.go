package executor

import (
	"strings"
	"testing"
)

func TestAnnotateRequestMemoryHintsMarksImageRequestsHeavy(t *testing.T) {
	metadata := AnnotateRequestMemoryHints(nil, []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`))

	if !IsHeavyRequest(Options{Metadata: metadata}) {
		t.Fatalf("expected image request to be marked heavy: %#v", metadata)
	}
	if !RequestHasImage(Options{Metadata: metadata}) {
		t.Fatalf("expected image hint to be recorded: %#v", metadata)
	}
}

func TestAnnotateRequestMemoryHintsMarksLargeRequestsHeavy(t *testing.T) {
	body := []byte(`{"input":"` + strings.Repeat("a", LargeRequestBodyThresholdBytes) + `"}`)
	metadata := AnnotateRequestMemoryHints(map[string]any{"existing": true}, body)

	if !IsHeavyRequest(Options{Metadata: metadata}) {
		t.Fatalf("expected large request to be marked heavy: %#v", metadata)
	}
	if got := RequestBodyBytes(Options{Metadata: metadata}); got != len(body) {
		t.Fatalf("request body bytes = %d, want %d", got, len(body))
	}
	if metadata["existing"] != true {
		t.Fatalf("existing metadata was not preserved: %#v", metadata)
	}
}
