package executor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestDevinExecutorUsageAcrossProtocols(t *testing.T) {
	for _, sample := range []struct {
		name, model                            string
		input, read, write, output, totalInput int64
		total                                  int64
	}{
		// Captured GPT usage plus synthetic cache boundary cases; no request content.
		{"gpt_cached", "devin/gpt-6-1-sol", 3, 428456, 7383, 2736, 435842, 438578},
		{"gpt_cold", "devin/gpt-6-astra", 3, 0, 193148, 285, 193151, 193436},
		{"claude_mixed", "devin/claude-sonnet-5", 1, 46733, 1000, 11, 47734, 47745},
		{"fully_cached", "devin/claude-sonnet-5", 0, 46733, 0, 11, 46733, 46744},
		{"uncached", "devin/gpt-6-astra", 17, 0, 0, 4, 17, 21},
		{"cache_write_only", "devin/claude-sonnet-5", 0, 0, 1000, 11, 1000, 1011},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var wireUsage []byte
			for _, field := range []struct {
				number protowire.Number
				value  int64
			}{{2, sample.input}, {3, sample.output}, {4, sample.write}, {5, sample.read}} {
				wireUsage = protowire.AppendTag(wireUsage, field.number, protowire.VarintType)
				wireUsage = protowire.AppendVarint(wireUsage, uint64(field.value))
			}
			frame := protowire.AppendTag(nil, 7, protowire.BytesType)
			frame = protowire.AppendBytes(frame, wireUsage)
			body := append(helps.WrapConnectEnvelope(frame), helps.WrapConnectEnvelopeWithFlag(helps.ConnectFlagEndStream, []byte(`{}`))...)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/connect+proto")
				_, _ = w.Write(body)
			}))
			defer server.Close()
			auth := &cliproxyauth.Auth{ID: "usage-test", Provider: "devin", Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}

			for _, protocol := range []struct {
				format                                     sdktranslator.Format
				inputPath, outputPath, readPath, writePath string
			}{
				{sdktranslator.FormatOpenAI, "prompt_tokens", "completion_tokens", "prompt_tokens_details.cached_tokens", "prompt_tokens_details.cache_write_tokens"},
				{sdktranslator.FormatOpenAIResponse, "input_tokens", "output_tokens", "input_tokens_details.cached_tokens", "input_tokens_details.cache_write_tokens"},
				{sdktranslator.FormatClaude, "input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"},
			} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/stream=%t", protocol.format, stream), func(t *testing.T) {
						payload := []byte(fmt.Sprintf(`{"model":%q,"stream":%t,"max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`, sample.model, stream))
						if protocol.format == sdktranslator.FormatOpenAIResponse {
							payload = []byte(fmt.Sprintf(`{"model":%q,"stream":%t,"input":"hello"}`, sample.model, stream))
						}
						if protocol.format == sdktranslator.FormatOpenAI && stream {
							payload, _ = sjson.SetBytes(payload, "stream_options.include_usage", true)
						}
						req := cliproxyexecutor.Request{Model: sample.model, Payload: payload}
						opts := cliproxyexecutor.Options{SourceFormat: protocol.format, OriginalRequest: payload, Stream: stream}
						exec := NewDevinExecutor(&config.Config{})
						var actual gjson.Result
						if stream {
							response, err := exec.ExecuteStream(context.Background(), auth, req, opts)
							if err != nil {
								t.Fatal(err)
							}
							for chunk := range response.Chunks {
								if chunk.Err != nil {
									t.Fatal(chunk.Err)
								}
								for line := range strings.SplitSeq(string(chunk.Payload), "\n") {
									data := strings.TrimPrefix(line, "data: ")
									path := "usage"
									if protocol.format == sdktranslator.FormatOpenAIResponse {
										path = "response.usage"
									}
									if usage := gjson.Get(data, path); usage.IsObject() {
										actual = usage
									}
								}
							}
						} else {
							response, err := exec.Execute(context.Background(), auth, req, opts)
							if err != nil {
								t.Fatal(err)
							}
							actual = gjson.GetBytes(response.Payload, "usage")
						}
						wantInput := sample.totalInput
						if protocol.format == sdktranslator.FormatClaude {
							wantInput = sample.input
						}
						for path, want := range map[string]int64{protocol.inputPath: wantInput, protocol.outputPath: sample.output} {
							if got := actual.Get(path); !got.Exists() || got.Int() != want {
								t.Errorf("%s = %s, want %d", path, got.Raw, want)
							}
						}
						for path, want := range map[string]int64{protocol.readPath: sample.read, protocol.writePath: sample.write} {
							if got := actual.Get(path).Int(); got != want {
								t.Errorf("%s = %d, want %d", path, got, want)
							}
						}
						if protocol.format != sdktranslator.FormatClaude && actual.Get("total_tokens").Int() != sample.total {
							t.Errorf("total_tokens = %d, want %d", actual.Get("total_tokens").Int(), sample.total)
						}
					})
				}
			}
		})
	}
}
