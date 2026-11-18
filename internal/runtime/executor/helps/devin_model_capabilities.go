package helps

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

// DevinUnsupportedImageError rejects image input for a model whose catalog
// metadata explicitly marks it text-only. It is request-scoped and maps to
// HTTP 400 so the client gets a clear capability error instead of a 502 from
// the upstream invalid_argument response.
type DevinUnsupportedImageError struct {
	Model string
}

func (e *DevinUnsupportedImageError) Error() string {
	if e == nil || e.Model == "" {
		return "model does not support image input"
	}
	return fmt.Sprintf("model %s does not support image input", e.Model)
}

func (e *DevinUnsupportedImageError) StatusCode() int { return http.StatusBadRequest }

func (e *DevinUnsupportedImageError) IsRequestScoped() bool { return true }

// CheckDevinImageInputPayload refuses image input only when the finalized
// business payload still carries images and the finalized model is explicitly
// text-only. It runs after payload rules, so rules that remove the images or
// switch to an image-capable model are honored; rules that add images are
// checked too. Missing capability metadata stays permissive.
func CheckDevinImageInputPayload(payload []byte) error {
	model := strings.TrimSpace(ojson.GetBytes(payload, "model").String())
	if model == "" {
		return nil
	}
	hasImages := false
	for _, prompt := range ojson.GetBytes(payload, "prompts").Array() {
		if len(prompt.Get("images").Array()) > 0 {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return nil
	}
	if !registry.DevinModelIsTextOnly(model) {
		return nil
	}
	return &DevinUnsupportedImageError{Model: model}
}
