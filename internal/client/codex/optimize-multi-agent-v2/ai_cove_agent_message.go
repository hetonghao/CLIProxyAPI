package multiagentv2

import "github.com/MoeForge/moego/ojson"

// RewriteCodexAgentMessageInputDoc unconditionally converts agent_message items
// into portable Responses message/user input in place, ignoring the client UA
// and the optimize-multi-agent-v2 switch. AI Cove callers such as xAI need this
// conversion on every request.
func RewriteCodexAgentMessageInputDoc(doc *ojson.Doc) bool {
	return rewriteCodexAgentMessageInputDoc(doc, true, false)
}
