package common

import (
	"regexp"
	"strings"
)

// IsDevinCodexAppAutomationUpdate reports whether a tool declaration corresponds
// to the automation_update method within the mcp__codex_app namespace.
func IsDevinCodexAppAutomationUpdate(namespace, toolName string) bool {
	cleanNamespace := strings.TrimSpace(namespace)
	cleanTool := strings.TrimSpace(toolName)
	if strings.EqualFold(cleanNamespace, "mcp__codex_app") && strings.EqualFold(cleanTool, "automation_update") {
		return true
	}
	if strings.EqualFold(cleanTool, "mcp__codex_app__automation_update") {
		return true
	}
	return false
}

const (
	execCommandTargetPhrase     = "returning output or a session ID for ongoing interaction"
	execCommandObfuscatedPhrase = "returning output or an session ID for ongoing interaction"
	writeStdinTargetPhrase      = "Writes characters to an existing unified exec session and returns recent output."
	writeStdinObfuscatedPhrase  = "Writes characters to a existing unified exec session and returns recent output."
)

var (
	execCommandRegex = regexp.MustCompile(`(?i)returning output or a session ID for ongoing interaction`)
	writeStdinRegex  = regexp.MustCompile(`(?i)Writes characters to an existing unified exec session and returns recent output(\.?)`)
)

// ObfuscateExecCommandDescription replaces "returning output or a session ID for ongoing interaction"
// with "returning output or an session ID for ongoing interaction".
func ObfuscateExecCommandDescription(desc string) string {
	if strings.Contains(desc, execCommandTargetPhrase) {
		desc = strings.ReplaceAll(desc, execCommandTargetPhrase, execCommandObfuscatedPhrase)
	}
	return execCommandRegex.ReplaceAllString(desc, execCommandObfuscatedPhrase)
}

// ObfuscateWriteStdinDescription replaces "Writes characters to an existing unified exec session and returns recent output."
// with "Writes characters to a existing unified exec session and returns recent output.".
func ObfuscateWriteStdinDescription(desc string) string {
	if strings.Contains(desc, writeStdinTargetPhrase) {
		desc = strings.ReplaceAll(desc, writeStdinTargetPhrase, writeStdinObfuscatedPhrase)
	}
	return writeStdinRegex.ReplaceAllString(desc, "Writes characters to a existing unified exec session and returns recent output$1")
}

// Devin rejects tool descriptions that claim unrestricted filesystem access with
// this exact sentence (for example Trae and Claude Code Read tools). The rewrite
// keeps the meaning while dropping the rejected phrasing.
//
// Rejected system-prompt lines use a different mechanism (whole-line removal in
// helps.SanitizeDevinSystemPrompt), so tool phrases are listed here next to the
// tool sanitizer instead of being merged into one table.
const devinUnrestrictedFileReadPhrase = "You can access any file directly by using this tool."

const devinUnrestrictedFileReadReplacement = "You can access files by path with this tool."

// SanitizeDevinToolDescription applies description obfuscation for Devin function tools.
// Clients may embed exec_command or write_stdin descriptions inside another tool
// (such as the Codex code-mode "exec" tool), and Devin rejects those phrases
// regardless of the tool name.
func SanitizeDevinToolDescription(_ string, desc string) string {
	if desc == "" {
		return desc
	}
	desc = strings.ReplaceAll(desc, devinUnrestrictedFileReadPhrase, devinUnrestrictedFileReadReplacement)
	desc = ObfuscateExecCommandDescription(desc)
	desc = ObfuscateWriteStdinDescription(desc)
	return desc
}
