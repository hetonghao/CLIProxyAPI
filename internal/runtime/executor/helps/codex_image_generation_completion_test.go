package helps

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestNormalizeCodexImageGenerationCompletion_ChangesOnlySuccessfulTerminalImageItems(t *testing.T) {
	// Given
	tests := []struct {
		name       string
		event      string
		statusPath string
		wantStatus string
	}{
		{
			name:       "completed output item with result",
			event:      `{"type":"response.completed","response":{"output":[{"type":"image_generation_call","status":"generating","result":"image-data"}]}}`,
			statusPath: "response.output.0.status",
			wantStatus: "completed",
		},
		{
			name:       "websocket done output item with result",
			event:      `{"type":"response.done","response":{"output":[{"type":"image_generation_call","status":"generating","result":"image-data"}]}}`,
			statusPath: "response.output.0.status",
			wantStatus: "completed",
		},
		{
			name:       "done item without result",
			event:      `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating"}}`,
			statusPath: "item.status",
			wantStatus: "generating",
		},
		{
			name:       "done item with blank result",
			event:      `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating","result":"  "}}`,
			statusPath: "item.status",
			wantStatus: "generating",
		},
		{
			name:       "done item with non-string result",
			event:      `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating","result":{"unexpected":true}}}`,
			statusPath: "item.status",
			wantStatus: "generating",
		},
		{
			name:       "done item with incomplete status",
			event:      `{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"incomplete","result":"image-data"}}`,
			statusPath: "item.status",
			wantStatus: "incomplete",
		},
		{
			name:       "done non-image item",
			event:      `{"type":"response.output_item.done","item":{"type":"function_call","status":"generating","result":"image-data"}}`,
			statusPath: "item.status",
			wantStatus: "generating",
		},
		{
			name:       "incomplete response",
			event:      `{"type":"response.incomplete","response":{"output":[{"type":"image_generation_call","status":"generating","result":"image-data"}]}}`,
			statusPath: "response.output.0.status",
			wantStatus: "generating",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got := NormalizeCodexImageGenerationCompletion([]byte(tt.event))

			// Then
			if status := gjson.GetBytes(got, tt.statusPath).String(); status != tt.wantStatus {
				t.Fatalf("status = %q, want %q; event=%s", status, tt.wantStatus, got)
			}
		})
	}
}

func TestNormalizeCodexImageGenerationCompletion_PreservesMalformedJSON(t *testing.T) {
	// Given
	event := []byte(`{"type":"response.output_item.done"`)

	// When
	got := NormalizeCodexImageGenerationCompletion(event)

	// Then
	if string(got) != string(event) {
		t.Fatalf("malformed event changed from %q to %q", event, got)
	}
}
