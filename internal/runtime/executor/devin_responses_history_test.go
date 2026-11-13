package executor

import (
	"bytes"
	"testing"

	"github.com/MoeForge/moego/base64"
	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestDevinResponsesHistoryPreservesReasoningOnUpstreamSwitch(t *testing.T) {
	envelope := make([]byte, 1+8+16+16+32)
	envelope[0] = 0x80
	for i := 9; i < len(envelope); i++ {
		envelope[i] = byte(i)
	}
	openaiSignature := base64.URLEncoding.EncodeToString(envelope)
	for _, tc := range []struct {
		name, model, signature, content, wantContent, wantThinking, wantSignatureType string
	}{
		{name: "summary only", model: "gpt-6.1-sol", wantContent: "Previous reasoning summary:\nKeep recovery code J7Q4M9."},
		{name: "native OpenAI reasoning", model: "gpt-6.1-sol", signature: openaiSignature, wantThinking: "Keep recovery code J7Q4M9.", wantSignatureType: "openai"},
		{name: "native Devin reasoning", model: "swe-2", signature: "sealed.v1.test", wantThinking: "Keep recovery code J7Q4M9.", wantSignatureType: "sealed"},
		{name: "foreign signature keeps summary", model: "gpt-6.1-sol", signature: "foreign-signature", wantContent: "Previous reasoning summary:\nKeep recovery code J7Q4M9."},
		{name: "OpenAI signature on another model", model: "swe-2", signature: openaiSignature, wantContent: "Previous reasoning summary:\nKeep recovery code J7Q4M9."},
		{name: "reasoning text survives", model: "gpt-6.1-sol", content: `[{"type":"reasoning_text","text":"Verify the lookup result."}]`, wantContent: "Previous reasoning summary:\nKeep recovery code J7Q4M9.\n\nVerify the lookup result."},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/http", true: "/stream"}[stream], func(t *testing.T) {
				item := ojson.NewDocString(`{"type":"reasoning","id":"rs_prior","summary":[{"type":"summary_text","text":"Keep recovery code J7Q4M9."}]}`)
				if err := item.Set("encrypted_content", tc.signature); err != nil {
					t.Fatal(err)
				}
				if tc.content != "" {
					if err := item.SetRaw("content", tc.content); err != nil {
						t.Fatal(err)
					}
				}
				payload := []byte(`{"input":[{"role":"user","content":"Look up the recovery result."},` + item.String() + `,{"type":"function_call","name":"lookup","call_id":"call_lookup","arguments":"{\"query\":\"J7Q4M9\"}"},{"type":"function_call_output","call_id":"call_lookup","output":"verified"},{"type":"custom_tool_call","name":"record","call_id":"call_record","input":"J7Q4M9"},{"type":"custom_tool_call_output","call_id":"call_record","output":"recorded"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}},{"type":"custom","name":"record"}],"reasoning":{"effort":"high"}}`)
				original := bytes.Clone(payload)
				wire := executeDevinSchemaRequest(t, tc.model, payload, &config.Config{}, stream)
				prompts := ojson.GetBytes(wire, "prompts").Array()
				if len(prompts) != 5 {
					t.Fatalf("got %d prompts, want user and two call/result pairs", len(prompts))
				}
				want := map[string]string{
					"content": tc.wantContent, "thinking": tc.wantThinking, "signature_type": tc.wantSignatureType,
					"tool_calls.0.id": "call_lookup", "tool_calls.0.arguments": `{"query":"J7Q4M9"}`,
				}
				for path, value := range want {
					if got := prompts[1].Get(path).String(); got != value {
						t.Errorf("assistant %s = %q, want %q", path, got, value)
					}
				}
				if tc.wantSignatureType == "" {
					if prompts[1].Get("signature").Exists() {
						t.Error("foreign signature reached the upstream")
					}
				} else if got := prompts[1].Get("signature").String(); got != base64.StdEncoding.EncodeToString([]byte(tc.signature)) {
					t.Errorf("signature = %q, want original signature", got)
				}
				if prompts[2].Get("tool_call_id").String() != "call_lookup" || prompts[2].Get("content").String() != "verified" {
					t.Error("tool result or call association changed")
				}
				if prompts[3].Get("tool_calls.0.id").String() != "call_record" || prompts[3].Get("tool_calls.0.arguments").String() != `{"input":"J7Q4M9"}` || prompts[4].Get("tool_call_id").String() != "call_record" || prompts[4].Get("content").String() != "recorded" {
					t.Error("custom tool input, output or call association changed")
				}
				if !bytes.Equal(original, payload) {
					t.Error("another upstream attempt needs the original ciphertext and history")
				}
			})
		}
	}
}
