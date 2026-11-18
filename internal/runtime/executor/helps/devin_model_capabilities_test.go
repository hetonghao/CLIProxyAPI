package helps

import (
	"errors"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

func TestCheckDevinImageInputPayload(t *testing.T) {
	reg := registry.GetGlobalRegistry()
	clientID := "test-devin-image-capabilities"
	models := []*registry.ModelInfo{
		{
			ID:                       "devin/swe-2-flash",
			Object:                   "model",
			Type:                     "devin",
			SupportedInputModalities: []string{"text"},
			ExplicitInputModalities:  true,
		},
		{
			ID:                       "swe-2-flash",
			Object:                   "model",
			Type:                     "devin",
			SupportedInputModalities: []string{"text"},
			ExplicitInputModalities:  true,
		},
		{
			ID:                       "devin/other-text-only",
			Object:                   "model",
			Type:                     "devin",
			SupportedInputModalities: []string{"text"},
			ExplicitInputModalities:  true,
		},
		{
			ID:                       "devin/test-image",
			Object:                   "model",
			Type:                     "devin",
			SupportedInputModalities: []string{"text", "image"},
			ExplicitInputModalities:  true,
		},
	}
	reg.RegisterClient(clientID, "devin", models)
	t.Cleanup(func() { reg.UnregisterClient(clientID) })

	imagePayload := func(model string) []byte {
		return []byte(`{"model":"` + model + `","prompts":[{"images":[{"mime_type":"image/png","data":"iVBORw0KGgo="}]}]}`)
	}
	textPayload := func(model string) []byte {
		return []byte(`{"model":"` + model + `","prompts":[{"content":"hello"}]}`)
	}

	for _, model := range []string{"devin/swe-2-flash", "swe-2-flash"} {
		err := CheckDevinImageInputPayload(imagePayload(model))
		var unsupported *DevinUnsupportedImageError
		if !errors.As(err, &unsupported) {
			t.Fatalf("%s image error = %v, want *DevinUnsupportedImageError", model, err)
		}
		if unsupported.StatusCode() != http.StatusBadRequest || !unsupported.IsRequestScoped() {
			t.Fatalf("%s error status/request-scoped = %d/%v", model, unsupported.StatusCode(), unsupported.IsRequestScoped())
		}
	}
	if err := CheckDevinImageInputPayload(textPayload("devin/swe-2-flash")); err != nil {
		t.Fatalf("text-only model text prompt rejected: %v", err)
	}
	if err := CheckDevinImageInputPayload(imagePayload("devin/test-image")); err != nil {
		t.Fatalf("image-capable model image prompt rejected: %v", err)
	}
	if err := CheckDevinImageInputPayload(imagePayload("devin/unknown-model")); err != nil {
		t.Fatalf("unknown model image prompt must stay permissive: %v", err)
	}
	if err := CheckDevinImageInputPayload(imagePayload("devin/other-text-only")); err != nil {
		t.Fatalf("non-target text-only model must stay out of scope: %v", err)
	}
	if err := CheckDevinImageInputPayload([]byte(`{"prompts":[{"images":[{"data":"x"}]}]}`)); err != nil {
		t.Fatalf("payload without a model must stay permissive: %v", err)
	}
}
