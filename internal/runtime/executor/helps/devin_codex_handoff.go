package helps

import (
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

// HasPendingEncryptedHandoff rejects unreadable mail in the current delivery batch.
// More mail or configuration can follow it before a user or assistant advances the conversation.
func HasPendingEncryptedHandoff(body []byte) bool {
	var pending bool
	ojson.GetBytesNoCopy(body, "input").ForEach(func(_, item ojson.Result) bool {
		switch item.Get("type").String() {
		case "agent_message":
			if !pending {
				item.Get("content").ForEach(func(_, part ojson.Result) bool {
					pending = part.Get("type").String() == "encrypted_content"
					return !pending
				})
			}
		case "message", "":
			role := item.Get("role").String()
			if role == "assistant" || role == "user" {
				pending = false
			}
		case "function_call", "custom_tool_call", "tool_search_call", "function_call_output", "custom_tool_call_output", "tool_search_output", "output_text":
			pending = false
		}
		return true
	})
	return pending
}
