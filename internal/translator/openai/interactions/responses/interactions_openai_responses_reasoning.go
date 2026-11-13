package responses

import (
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/signature"
	translatorcommon "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/common"
)

// Keep native state when Devin can replay it; unsigned summaries are history,
// not a substitute for a provider's opaque reasoning state.
func responsesReasoningToInteractions(item ojson.Result, modelName string) []byte {
	var texts []string
	for _, field := range []string{"summary", "content"} {
		for _, part := range item.Get(field).Array() {
			if text := part.Get("text").String(); text != "" {
				texts = append(texts, text)
			}
		}
	}
	sig := strings.TrimSpace(item.Get("encrypted_content").String())
	if !strings.HasPrefix(sig, "sealed.v1.") {
		sig = ""
		if !isAntigravityModel(modelName) && signature.SignatureProviderFromModelName(modelName) == signature.SignatureProviderGPT {
			sig, _ = translatorcommon.CodexReplaySignatureForTarget(modelName, item.Get("encrypted_content").String())
		}
	}
	text := strings.Join(texts, "\n\n")
	if sig == "" {
		if text == "" || isAntigravityModel(modelName) {
			return nil
		}
		return interactionsTextStep("model_output", "Previous reasoning summary:\n"+text)
	}
	stepDoc := ojson.NewDocString(`{"type":"thought","content":[{"type":"text","text":""}]}`)
	if err := stepDoc.Set("content.0.text", text); err != nil {
		return nil
	}
	if err := stepDoc.Set("signature", sig); err != nil {
		return nil
	}
	return stepDoc.Bytes()
}
