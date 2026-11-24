package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"google.golang.org/protobuf/encoding/protowire"
)

const handoffFunction = `{"type":"function","name":"send_message","parameters":{"type":"object","properties":{"target":{"type":"string"},"message":{"type":"string","encrypted":true,"description":"original help"}},"required":["target","message"]}}`

func TestDevinCodexHandoffRequest(t *testing.T) {
	const ordinary = `{"type":"namespace","name":"other","tools":[` + handoffFunction + `]}`
	const collab = `{"type":"namespace","name":"collaboration","tools":[` + handoffFunction + `]}`
	for _, tc := range []struct {
		name, decl string
		recognized bool
	}{
		{"normal declaration", `"tools":[` + ordinary + ` ,` + collab + `],"input":[{"role":"user","content":"ping"}]`, true},
		{"deferred declaration", `"tools":[` + ordinary + `],"input":[{"role":"user","content":"ping"},{"type":"additional_tools","tools":[` + collab + `]}]`, true},
		{"flat tool shadows namespace", `"tools":[` + ordinary + `,{"type":"function","name":"collaboration__send_message","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true}}}},` + collab + `],"input":[{"role":"user","content":"ping"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(`{"model":"gpt-6.2-sol",` + tc.decl + `}`)
			original := bytes.Clone(payload)
			wire := executeDevinSchemaRequest(t, "gpt-6.2-sol", payload, &config.Config{}, true)
			got := ojson.GetBytes(wire, "tools")
			var checked int
			for _, tool := range got.Array() {
				name := tool.Get("name").String()
				enc := tool.Get("parameters.properties.message.encrypted")
				switch name {
				case "collaboration__send_message":
					if enc.Exists() == tc.recognized {
						t.Errorf("handoff encryption flag = %s, recognized = %v", enc.Raw, tc.recognized)
					}
					if tc.recognized && tool.Get("parameters.properties.message.description").String() != "original help" {
						t.Error("lost message description")
					}
					checked++
				case "other__send_message":
					if !enc.Bool() {
						t.Error("overrode unrelated namespace")
					}
					checked++
				}
			}
			if checked != 2 || !bytes.Equal(payload, original) {
				t.Fatalf("tools checked = %d; caller payload must remain reusable", checked)
			}
		})
	}
}

func TestDevinCodexHandoffResponse(t *testing.T) {
	const marker = "handoff-receipt / β"
	for _, tc := range []struct {
		name, ns, extra string
		cfg             *config.Config
		marked          bool
	}{
		{"spawn_agent", "collaboration", "", nil, true}, {"send_message", "collaboration", "", nil, true}, {"followup_task", "collaboration", "", nil, true},
		{"send_message", "other", "", nil, false}, {"list_agents", "collaboration", "", nil, false},
		{"send_message", "collaboration", `,"private":{"type":"string","encrypted":true}`, nil, false},
		{"spawn_agent", "collaboration", "", &config.Config{Payload: config.PayloadConfig{Override: []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "*"}}, Params: map[string]any{"tools.0.parameters.properties.message.encrypted": true}}}}}, false},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/stream=%t/extra=%t/override=%t", tc.ns, tc.name, stream, tc.extra != "", tc.cfg != nil), func(t *testing.T) {
				fn := strings.Replace(handoffFunction, "send_message", tc.name, 1)
				fn = strings.Replace(fn, `},"required"`, tc.extra+`},"required"`, 1)
				payload := []byte(`{"input":[{"role":"user","content":"ping"},{"type":"additional_tools","tools":[{"type":"namespace","name":"` + tc.ns + `","tools":[` + fn + `]}]}]}`)
				cfg := tc.cfg
				if cfg == nil {
					cfg = &config.Config{}
				}
				transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					var call []byte
					for i, s := range []string{"call_rcpt", tc.ns + "__" + tc.name, `{"message":"` + marker + `"}`} {
						call = protowire.AppendString(protowire.AppendTag(call, protowire.Number(i+1), protowire.BytesType), s)
					}
					frame := protowire.AppendBytes(protowire.AppendTag(nil, 6, protowire.BytesType), call)
					frames := append(helps.WrapConnectEnvelope(frame), devinSchemaCompletionFrames()...)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/connect+proto"}}, Body: io.NopCloser(bytes.NewReader(frames))}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", transport)
				e := NewDevinExecutor(cfg)
				auth := &cliproxyauth.Auth{ID: t.Name(), Provider: "devin", Attributes: map[string]string{"api_key": "test-key"}}
				req := cliproxyexecutor.Request{Model: "gpt-6.2-sol", Payload: payload}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Stream: stream}
				check := func(phase string, item ojson.Result) {
					t.Helper()
					if item.Get("namespace").String() != tc.ns || item.Get("name").String() != tc.name || item.Get("call_id").String() != "call_rcpt" {
						t.Fatalf("%s: incorrect tool identity %s", phase, item.Raw)
					}
					m := item.Get("encrypted_function_args")
					if (m.Raw == "[]") != tc.marked || (!tc.marked && m.Exists()) {
						t.Errorf("%s: encrypted_function_args = %q, want explicit empty array = %v", phase, m.Raw, tc.marked)
					}
					if phase != "added" && ojson.Get(item.Get("arguments").String(), "message").String() != marker {
						t.Fatalf("%s: lost original argument", phase)
					}
				}
				if !stream {
					response, err := e.Execute(ctx, auth, req, opts)
					if err != nil {
						t.Fatal(err)
					}
					check("output", ojson.GetBytes(response.Payload, `output.#(type=="function_call")`))
					return
				}
				response, err := e.ExecuteStream(ctx, auth, req, opts)
				if err != nil {
					t.Fatal(err)
				}
				seen := map[string]bool{}
				for chunk := range response.Chunks {
					if chunk.Err != nil {
						t.Fatal(chunk.Err)
					}
					for _, line := range strings.Split(string(chunk.Payload), "\n") {
						data, ok := strings.CutPrefix(line, "data: ")
						if !ok {
							continue
						}
						event := ojson.Parse(data)
						phase := event.Get("type").String()
						switch phase {
						case "response.output_item.added", "response.output_item.done":
							item := event.Get("item")
							if item.Get("type").String() == "function_call" {
								label := strings.TrimPrefix(phase, "response.output_item.")
								check(label, item)
								seen[label] = true
							}
						case "response.completed":
							check("completed", event.Get(`response.output.#(type=="function_call")`))
							seen["completed"] = true
						}
					}
				}
				if len(seen) != 3 {
					t.Fatalf("missing streaming phases: %v", seen)
				}
			})
		}
	}
}

func TestDevinRejectsOnlyPendingEncryptedHandoff(t *testing.T) {
	const encrypted = `{"type":"agent_message","author":"/root","recipient":"/root/w","content":[{"type":"input_text","text":"Message Type: NEW_TASK\nPayload:\n"},{"type":"encrypted_content","encrypted_content":"private-old-cipher"}]}`
	const clear = `{"type":"agent_message","author":"/root","recipient":"/root/w","content":[{"type":"input_text","text":"Message Type: NEW_TASK\nPayload:\nnew-readable-receipt"}]}`
	const turn = `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"old task was blocked"}]}`
	for _, tc := range []struct {
		name, items string
		refuse      bool
	}{
		{"current", encrypted, true}, {"current followed by configuration", encrypted + `,{"type":"additional_tools","tools":[]}`, true},
		{"escaped task type", strings.Replace(encrypted, "agent_message", `agent_\u006dessage`, 1), true},
		{"batched unread task and readable notice", encrypted + "," + clear, true},
		{"unread task before developer context", encrypted + `,{"type":"message","role":"developer","content":[{"type":"input_text","text":"status context"}]}`, true},
		{"history before new readable task", encrypted + "," + turn + "," + clear, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(`{"input":[` + tc.items + `]}`)
			if !tc.refuse {
				wire := executeDevinSchemaRequest(t, "gpt-6.2-sol", payload, &config.Config{}, false)
				prompts := ojson.GetBytes(wire, "prompts").Array()
				if !strings.Contains(prompts[len(prompts)-1].Get("content").String(), "new-readable-receipt") {
					t.Fatal("new task lost after historical encrypted task")
				}
				return
			}
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripperFunc(func(*http.Request) (*http.Response, error) {
				t.Error("unreadable task should never be sent to Devin")
				return nil, errors.New("unexpected upstream request")
			}))
			e := NewDevinExecutor(&config.Config{})
			_, err := e.Execute(ctx, &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "test-key"}}, cliproxyexecutor.Request{Model: "gpt-6.2-sol", Payload: payload}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
			var status interface{ StatusCode() int }
			if !errors.As(err, &status) || status.StatusCode() != 400 || !strings.Contains(err.Error(), "resend") || strings.Contains(err.Error(), "private-old-cipher") {
				t.Fatalf("expected actionable redacted 400, got %v", err)
			}
		})
	}
}
