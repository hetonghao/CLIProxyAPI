package helps

import (
	"bytes"
	"context"
	"strings"

	"github.com/MoeForge/moego/ojson"
	translatorcommon "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
)

// PrepareDevinHandoffs selects tools by their original, winning Responses declaration.
// A flat function with the same upstream name is a different client contract.
func PrepareDevinHandoffs(tools []DevinTool, source []byte) map[string]bool {
	var eligible map[string]bool
	var winners map[string]util.ResponsesToolDescriptor
	for i := range tools {
		tool := &tools[i]
		ns, name, ok := strings.Cut(tool.Name, "__")
		if !ok || ns != "collaboration" {
			continue
		}
		switch name {
		case "spawn_agent", "send_message", "followup_task":
		default:
			continue
		}
		if winners == nil {
			winners = util.CollectResponsesToolWinners(ojson.ParseBytes(source))
		}
		decl := winners[tool.Name]
		if decl.Namespace != ns || decl.LocalName != name || decl.ToolType != "function" {
			continue
		}
		schema := ojson.NewDoc(tool.Parameters)
		field := schema.Get("properties.message")
		if !field.IsObject() || field.Get("type").String() != "string" {
			continue
		}
		if field.Get("encrypted").Exists() {
			if err := schema.Delete("properties.message.encrypted"); err != nil {
				continue
			}
			tool.Parameters = schema.Bytes()
		}
		if eligible == nil {
			eligible = make(map[string]bool)
		}
		eligible[tool.Name] = true
	}
	return eligible
}

// WithDevinPlaintextHandoffs records only declarations that survived final payload rules.
// An empty encrypted_function_args array asserts that no argument was encrypted.
func WithDevinPlaintextHandoffs(ctx context.Context, eligible map[string]bool, final []byte) context.Context {
	if len(eligible) == 0 {
		return ctx
	}
	approved := make(map[string]bool, len(eligible))
	seen := make(map[string]bool, len(eligible))
	for _, t := range ojson.GetBytesNoCopy(final, "tools").Array() {
		name := t.Get("name").String()
		if !eligible[name] {
			continue
		}
		p := t.Get("parameters")
		if seen[name] || p.Get("properties.message.type").String() != "string" || encryptedParameter(p) {
			delete(approved, name)
		} else {
			approved[name] = true
		}
		seen[name] = true
	}
	return translatorcommon.WithDevinPlaintextHandoffs(ctx, approved)
}

func encryptedParameter(schema ojson.Result) bool {
	if !schema.IsObject() && !schema.IsArray() {
		return false
	}
	found := false
	schema.ForEach(func(k, v ojson.Result) bool {
		found = (k.String() == "encrypted" && v.Type != ojson.False) || encryptedParameter(v)
		return !found
	})
	return found
}

// HasPendingEncryptedHandoff checks the last conversational input, not historical mail.
// Configuration records may follow the current input without starting another turn.
func HasPendingEncryptedHandoff(body []byte) bool {
	if !bytes.Contains(body, []byte("agent_message")) {
		return false
	}
	var pending bool
	ojson.GetBytesNoCopy(body, "input").ForEach(func(_, item ojson.Result) bool {
		switch item.Get("type").String() {
		case "additional_tools", "configuration_update":
			return true
		case "agent_message":
			pending = false
			item.Get("content").ForEach(func(_, part ojson.Result) bool {
				pending = part.Get("type").String() == "encrypted_content"
				return !pending
			})
		default:
			pending = false
		}
		return true
	})
	return pending
}
