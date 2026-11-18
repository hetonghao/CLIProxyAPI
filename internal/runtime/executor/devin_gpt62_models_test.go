package executor

import (
	"fmt"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestDevinExecutorGPT62ModelUID(t *testing.T) {
	for _, family := range []string{"astra", "sol"} {
		for _, stream := range []bool{false, true} {
			for _, tt := range []struct{ name, model, effort, want string }{
				{"default medium", "devin/gpt-6-2-%s", "", "medium"},
				{"low", "devin/gpt-6-2-%s", "low", "low"},
				{"medium", "devin/gpt-6-2-%s", "medium", "medium"},
				{"high", "devin/gpt-6-2-%s", "high", "high"},
				{"xhigh", "devin/gpt-6-2-%s", "xhigh", "xhigh"},
				{"max", "devin/gpt-6-2-%s", "max", "max"},
				{"unsupported none uses default", "devin/gpt-6-2-%s", "none", "medium"},
				{"suffix overrides body", "devin/gpt-6-2-%s(low)", "high", "low"},
				{"dotted name", "gpt-6.2-%s", "high", "high"},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", family, stream, tt.name), func(t *testing.T) {
					model := fmt.Sprintf(tt.model, family)
					payload := []byte(fmt.Sprintf(`{"model":%q,"input":[{"role":"user","content":"OK"}],"reasoning":{"effort":%q}}`, model, tt.effort))
					wire := executeDevinSchemaRequest(t, model, payload, &config.Config{}, stream)
					want := "gpt-6-2-" + family + "-" + tt.want
					if got := ojson.GetBytes(wire, "model").String(); got != want {
						t.Fatalf("upstream model = %q, want %q", got, want)
					}
				})
			}
		}
	}
}
