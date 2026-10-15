package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestDevinExecutorGeminiEmptyStringEnum(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "execute", true: "stream"}[stream], func(t *testing.T) {
			payload := []byte(`{"input":[{"role":"user","content":"OK"}],"tools":[{"type":"function","name":"task_board_update","parameters":{"type":"object","properties":{"permission":{"type":"string","description":"An empty string restores the session default.","enum":["read-only","workspace-write","danger-full-access",""]}},"required":["permission"]}}]}`)
			wire := executeDevinSchemaRequest(t, "gemini-4-argon", payload, &config.Config{}, stream)
			permission := gjson.GetBytes(wire, "tools.0.parameters.properties.permission")
			if permission.Get("enum").Exists() {
				t.Fatalf("Gemini rejects empty-string enum members: %s", permission.Raw)
			}
			for _, text := range []string{"An empty string restores the session default.", `"read-only"`, `"workspace-write"`, `"danger-full-access"`, `""`} {
				if !strings.Contains(permission.Get("description").String(), text) {
					t.Errorf("description must preserve %q: %s", text, permission.Raw)
				}
			}
			if permission.Get("type").String() != "string" || gjson.GetBytes(wire, "tools.0.parameters.required.0").String() != "permission" {
				t.Fatalf("unrelated schema constraints changed: %s", wire)
			}
		})
	}
}

func TestDevinExecutorGeminiEnumScopeAndHistory(t *testing.T) {
	for _, model := range []string{"gemini-4-argon", "devin/gemini-4-argon(low)", "MODEL_GOOGLE_GEMINI_3_0_FLASH", "devin/claude-sonnet-5-5", "devin/gpt-6-1-sol", "devin/swe-2"} {
		t.Run(model, func(t *testing.T) {
			payload := []byte(`{"input":[{"type":"function_call","name":"task_board_update","call_id":"prior","arguments":"{\"taskId\":\"t\",\"permission\":\"\"}"},{"type":"function_call_output","call_id":"prior","output":"cleared"},{"role":"user","content":"OK"}],"tools":[{"type":"function","name":"task_board_update","parameters":{"type":"object","properties":{"permission":{"type":"string","enum":["read-only",""]},"status":{"type":"string","enum":["pending","done"]}}}}]}`)
			original := bytes.Clone(payload)
			wire := executeDevinSchemaRequest(t, model, payload, &config.Config{}, false)
			gemini := strings.Contains(model, "gemini") || strings.Contains(model, "GEMINI")
			if gjson.GetBytes(wire, "tools.0.parameters.properties.permission.enum").Exists() == gemini {
				t.Fatalf("empty enum rewrite must be Gemini-only: %s", wire)
			}
			if gjson.GetBytes(wire, "tools.0.parameters.properties.status.enum").Raw != `["pending","done"]` {
				t.Fatalf("ordinary enum changed: %s", wire)
			}
			found := false
			for _, prompt := range gjson.GetBytes(wire, "prompts").Array() {
				for _, call := range prompt.Get("tool_calls").Array() {
					args := gjson.Parse(call.Get("arguments").String())
					if args.Get("taskId").String() == "t" {
						found = args.Get("permission").Exists() && args.Get("permission").String() == ""
					}
				}
			}
			if !found || !bytes.Equal(payload, original) {
				t.Fatal("history arguments and caller payload must remain unchanged")
			}
		})
	}
}

func TestDevinExecutorGeminiNestedEnumsPreserveSchemaData(t *testing.T) {
	schema := `{"type":"object","properties":{"entries":{"type":"array","items":{"type":"object","properties":{"a.b":{"type":"string","enum":[""]}}}},"choice":{"anyOf":[{"type":"string","enum":["keep",""]},{"type":"null"}]}},"$defs":{"permission":{"type":"string","enum":["read-only",""]}},"additionalProperties":{"type":"string","enum":["extra",""]},"required":["entries"],"default":{"type":"string","enum":["", "literal"],"id":9007199254740993},"examples":[{"enum":["", "example"]}]}`
	payload := []byte(fmt.Sprintf(`{"input":[{"role":"user","content":"OK"}],"tools":[{"type":"function","name":"f","parameters":%s}]}`, schema))
	wire := executeDevinSchemaRequest(t, "gemini-4-argon", payload, &config.Config{}, true)
	parameters := gjson.GetBytes(wire, "tools.0.parameters")
	for _, path := range []string{`properties.entries.items.properties.a\.b`, "properties.choice.anyOf.0", "$defs.permission", "additionalProperties"} {
		if parameters.Get(path+".enum").Exists() || !strings.Contains(parameters.Get(path+".description").String(), `""`) {
			t.Errorf("nested empty enum not preserved as a hint at %s: %s", path, parameters.Raw)
		}
	}
	if parameters.Get("default.enum.0").String() != "" || !parameters.Get("default.enum.0").Exists() || parameters.Get("default.id").Raw != "9007199254740993" || parameters.Get("examples.0.enum.0").String() != "" || !parameters.Get("examples.0.enum.0").Exists() || parameters.Get("required.0").String() != "entries" {
		t.Fatalf("schema data or unrelated constraints changed: %s", parameters.Raw)
	}
}

func TestDevinExecutorGeminiEnumPayloadRulesRemainFinal(t *testing.T) {
	cfg := &config.Config{Payload: config.PayloadConfig{
		Override: []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "gemini-4-argon"}}, Params: map[string]any{"tools.0.parameters.properties.permission.enum": []string{"override", ""}}}},
		Filter:   []config.PayloadFilterRule{{Models: []config.PayloadModelRule{{Name: "gemini-4-argon"}}, Params: []string{"tools.0.parameters.properties.permission.description"}}},
	}}
	payload := []byte(`{"input":[{"role":"user","content":"OK"}],"tools":[{"type":"function","name":"f","parameters":{"type":"object","properties":{"permission":{"type":"string","enum":["read-only",""]}}}}]}`)
	for _, stream := range []bool{false, true} {
		wire := executeDevinSchemaRequest(t, "gemini-4-argon", payload, cfg, stream)
		permission := gjson.GetBytes(wire, "tools.0.parameters.properties.permission")
		if permission.Get("enum").Raw != `["override",""]` || permission.Get("description").Exists() {
			t.Fatalf("built-in compatibility must not undo payload rules: %s", permission.Raw)
		}
	}
}

func TestDevinExecutorGeminiPreservesNegativeAndConditionalEnums(t *testing.T) {
	payload := []byte(`{"input":[{"role":"user","content":"OK"}],"tools":[{"type":"function","name":"f","parameters":{"type":"object","properties":{"permission":{"type":"string","enum":["read-only",""]},"negative":{"type":"string","not":{"type":"string","enum":["forbidden",""]}},"conditional":{"if":{"type":"string","enum":["special",""]},"then":{"maxLength":4},"else":{"minLength":5}},"exclusive":{"oneOf":[{"type":"string","enum":["read",""]},{"type":"string","enum":["write"]}]},"bounded":{"type":"array","contains":{"type":"string","enum":["special",""]},"maxContains":1}}}}]}`)
	wire := executeDevinSchemaRequest(t, "gemini-4-argon", payload, &config.Config{}, false)
	parameters := gjson.GetBytes(wire, "tools.0.parameters")
	for path, want := range map[string]string{
		"properties.negative.not.enum":          `["forbidden",""]`,
		"properties.conditional.if.enum":        `["special",""]`,
		"properties.exclusive.oneOf.0.enum":     `["read",""]`,
		"properties.exclusive.oneOf.1.enum":     `["write"]`,
		"properties.conditional.then.maxLength": `4`,
		"properties.conditional.else.minLength": `5`,
		"properties.bounded.contains.enum":      `["special",""]`,
		"properties.bounded.maxContains":        `1`,
	} {
		if got := parameters.Get(path).Raw; got != want {
			t.Errorf("constraint %s = %s, want %s", path, got, want)
		}
	}
	if parameters.Get("properties.permission.enum").Exists() {
		t.Fatal("ordinary allowed-value enums still need compatibility handling")
	}
}

func TestDevinExecutorGeminiPreservesReferencedSchemas(t *testing.T) {
	for name, schema := range map[string]string{
		"negative definition":  `{"$defs":{"blocked":{"type":"string","enum":["forbidden",""]}},"type":"object","properties":{"permission":{"type":"string","not":{"$ref":"#/$defs/blocked"}}}}`,
		"exclusive definition": `{"definitions":{"choice":{"type":"string","enum":["read",""]}},"type":"object","properties":{"permission":{"oneOf":[{"$ref":"#/definitions/choice"},{"type":"string","enum":["write"]}]}}}`,
		"property reference":   `{"type":"object","properties":{"shared":{"type":"string","enum":["read",""]},"permission":{"not":{"$ref":"#/properties/shared"}}}}`,
		"escaped reference":    `{"type":"object","properties":{"shared":{"type":"string","enum":["read",""]},"permission":{"not":{"\u0024ref":"#/properties/shared"}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			payload := []byte(fmt.Sprintf(`{"input":[{"role":"user","content":"OK"}],"tools":[{"type":"function","name":"f","parameters":%s}]}`, schema))
			wire := executeDevinSchemaRequest(t, "gemini-4-argon", payload, &config.Config{}, false)
			got := gjson.GetBytes(wire, "tools.0.parameters")
			if !reflect.DeepEqual(got.Value(), gjson.Parse(schema).Value()) {
				t.Fatalf("shared constraints must remain unchanged: %s", got.Raw)
			}
		})
	}
}

func executeDevinSchemaRequest(t *testing.T, model string, payload []byte, cfg *config.Config, stream bool) []byte {
	t.Helper()
	var captured []byte
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		_, proto, errRead := helps.ReadConnectFrame(req.Body)
		if errRead != nil {
			return nil, errRead
		}
		_, view, errDecode := helps.FinalizeDevinPayload(proto, func(body []byte) []byte { return body })
		if errDecode != nil {
			return nil, errDecode
		}
		captured = view
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/connect+proto"}},
			Body:       io.NopCloser(bytes.NewReader(helps.WrapConnectEnvelopeWithFlag(helps.ConnectFlagEndStream, []byte(`{}`)))),
		}, nil
	})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", transport)
	exec := NewDevinExecutor(cfg)
	auth := &cliproxyauth.Auth{ID: t.Name(), Provider: "devin", Attributes: map[string]string{"api_key": "test-key"}}
	req := cliproxyexecutor.Request{Model: model, Payload: payload}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: payload}
	if stream {
		result, errStream := exec.ExecuteStream(ctx, auth, req, opts)
		if errStream != nil {
			t.Fatal(errStream)
		}
		for chunk := range result.Chunks {
			if chunk.Err != nil {
				t.Fatal(chunk.Err)
			}
		}
	} else if _, errExecute := exec.Execute(ctx, auth, req, opts); errExecute != nil {
		t.Fatal(errExecute)
	}
	if len(captured) == 0 {
		t.Fatal("no request reached the upstream transport")
	}
	return captured
}
