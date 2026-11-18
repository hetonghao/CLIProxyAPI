package common

import (
	"strings"
	"testing"
)

func TestIsDevinCodexAppAutomationUpdate(t *testing.T) {
	tests := []struct {
		namespace string
		toolName  string
		want      bool
	}{
		{namespace: "mcp__codex_app", toolName: "automation_update", want: true},
		{namespace: "MCP__CODEX_APP", toolName: "AUTOMATION_UPDATE", want: true},
		{namespace: " mcp__codex_app ", toolName: " automation_update ", want: true},
		{namespace: "", toolName: "mcp__codex_app__automation_update", want: true},
		{namespace: "other_namespace", toolName: "automation_update", want: false},
		{namespace: "mcp__codex_app", toolName: "exec_command", want: false},
		{namespace: "", toolName: "automation_update", want: false},
		{namespace: "", toolName: "exec_command", want: false},
	}

	for _, tt := range tests {
		got := IsDevinCodexAppAutomationUpdate(tt.namespace, tt.toolName)
		if got != tt.want {
			t.Errorf("IsDevinCodexAppAutomationUpdate(%q, %q) = %v, want %v", tt.namespace, tt.toolName, got, tt.want)
		}
	}
}

func TestObfuscateExecCommandDescription(t *testing.T) {
	const original = "Runs a command in a bash shell, returning output or a session ID for ongoing interaction."
	const want = "Runs a command in a bash shell, returning output or an session ID for ongoing interaction."

	got := ObfuscateExecCommandDescription(original)
	if got != want {
		t.Fatalf("ObfuscateExecCommandDescription() = %q, want %q", got, want)
	}

	// Idempotency: second pass does not change output
	gotAgain := ObfuscateExecCommandDescription(got)
	if gotAgain != want {
		t.Fatalf("ObfuscateExecCommandDescription() idempotent = %q, want %q", gotAgain, want)
	}

	// Unrelated description should be untouched
	unrelated := "Runs an arbitrary script in a container"
	if gotUnrelated := ObfuscateExecCommandDescription(unrelated); gotUnrelated != unrelated {
		t.Fatalf("ObfuscateExecCommandDescription() unrelated = %q, want %q", gotUnrelated, unrelated)
	}
}

func TestObfuscateWriteStdinDescription(t *testing.T) {
	const original = "Writes characters to an existing unified exec session and returns recent output."
	const want = "Writes characters to a existing unified exec session and returns recent output."

	got := ObfuscateWriteStdinDescription(original)
	if got != want {
		t.Fatalf("ObfuscateWriteStdinDescription() = %q, want %q", got, want)
	}

	// Idempotency: second pass does not change output
	gotAgain := ObfuscateWriteStdinDescription(got)
	if gotAgain != want {
		t.Fatalf("ObfuscateWriteStdinDescription() idempotent = %q, want %q", gotAgain, want)
	}

	// Unrelated description should be untouched
	unrelated := "Writes arbitrary input to a process"
	if gotUnrelated := ObfuscateWriteStdinDescription(unrelated); gotUnrelated != unrelated {
		t.Fatalf("ObfuscateWriteStdinDescription() unrelated = %q, want %q", gotUnrelated, unrelated)
	}
}

func TestSanitizeDevinToolDescription(t *testing.T) {
	execDesc := "Runs a command in a bash shell, returning output or a session ID for ongoing interaction."
	sanitizedExec := SanitizeDevinToolDescription("exec_command", execDesc)
	if !strings.Contains(sanitizedExec, "an session ID") {
		t.Errorf("SanitizeDevinToolDescription(exec_command) does not contain 'an session ID': %q", sanitizedExec)
	}

	qualifiedExec := SanitizeDevinToolDescription("mcp__codex_app__exec_command", execDesc)
	if !strings.Contains(qualifiedExec, "an session ID") {
		t.Errorf("SanitizeDevinToolDescription(mcp__codex_app__exec_command) does not contain 'an session ID': %q", qualifiedExec)
	}

	stdinDesc := "Writes characters to an existing unified exec session and returns recent output."
	sanitizedStdin := SanitizeDevinToolDescription("write_stdin", stdinDesc)
	if !strings.Contains(sanitizedStdin, "to a existing unified exec session") {
		t.Errorf("SanitizeDevinToolDescription(write_stdin) does not contain 'to a existing unified exec session': %q", sanitizedStdin)
	}

	codeModeExecDesc := "Run JavaScript code to orchestrate/compose tool calls\n" +
		"### exec_command\nRuns a command in a PTY, returning output or a session ID for ongoing interaction.\n" +
		"### write_stdin\nWrites characters to an existing unified exec session and returns recent output.\n"
	for _, toolName := range []string{"exec", "functions__exec", "other_tool"} {
		sanitized := SanitizeDevinToolDescription(toolName, codeModeExecDesc)
		if !strings.Contains(sanitized, "an session ID") {
			t.Errorf("SanitizeDevinToolDescription(%s) does not contain 'an session ID': %q", toolName, sanitized)
		}
		if !strings.Contains(sanitized, "to a existing unified exec session") {
			t.Errorf("SanitizeDevinToolDescription(%s) does not contain 'to a existing unified exec session': %q", toolName, sanitized)
		}
	}

	mixedDesc := "First occurrence: returning output or an session ID for ongoing interaction.\n" +
		"Second occurrence: returning output or a session ID for ongoing interaction.\n" +
		"First stdin: Writes characters to a existing unified exec session and returns recent output.\n" +
		"Second stdin: Writes characters to an existing unified exec session and returns recent output."
	sanitizedMixed := SanitizeDevinToolDescription("functions__exec", mixedDesc)
	if strings.Contains(sanitizedMixed, "a session ID") || strings.Contains(sanitizedMixed, "to an existing unified") {
		t.Errorf("SanitizeDevinToolDescription(functions__exec) left raw phrase in mixed description: %q", sanitizedMixed)
	}

	unrelatedDesc := "Writes arbitrary input to a process"
	sanitizedUnrelated := SanitizeDevinToolDescription("other_tool", unrelatedDesc)
	if sanitizedUnrelated != unrelatedDesc {
		t.Errorf("SanitizeDevinToolDescription(other_tool) modified unrelated description: %q", sanitizedUnrelated)
	}
}

func TestSanitizeDevinToolDescription_NeutralizesOpenClawBrowserPhrase(t *testing.T) {
	const desc = "Control the browser via OpenClaw's browser control server (status/start/stop/profiles/tabs/open/snapshot/screenshot/actions). Use snapshot+act for UI automation."
	sanitized := SanitizeDevinToolDescription("browser", desc)
	if strings.Contains(strings.ToLower(sanitized), "via openclaw") {
		t.Fatalf("Devin-rejected OpenClaw phrase survived sanitization: %q", sanitized)
	}
	if !strings.Contains(sanitized, "using OpenClaw") {
		t.Fatalf("expected neutral OpenClaw phrase in sanitized description: %q", sanitized)
	}
	if !strings.Contains(sanitized, "Use snapshot+act for UI automation.") {
		t.Fatalf("unrelated browser instructions were modified: %q", sanitized)
	}
	if again := SanitizeDevinToolDescription("browser", sanitized); again != sanitized {
		t.Fatalf("sanitizer is not idempotent: %q -> %q", sanitized, again)
	}

	lower := "control the browser via openclaw's browser control server"
	lowerSanitized := SanitizeDevinToolDescription("browser", lower)
	if strings.Contains(strings.ToLower(lowerSanitized), "via openclaw") {
		t.Fatalf("case-insensitive OpenClaw phrase survived sanitization: %q", lowerSanitized)
	}

	unrelated := "Control the browser via Foo"
	if got := SanitizeDevinToolDescription("browser", unrelated); got != unrelated {
		t.Fatalf("unrelated browser description was modified: %q", got)
	}
}

func TestSanitizeDevinToolDescription_NeutralizesReadAnyFilePhrase(t *testing.T) {
	desc := "Reads a file from the local filesystem. You can access any file directly by using this tool.\nUsage:\n- The file_path parameter must be an absolute path."
	sanitized := SanitizeDevinToolDescription("Read", desc)
	if strings.Contains(sanitized, "You can access any file directly by using this tool.") {
		t.Fatalf("Devin-rejected Read phrase survived sanitization: %q", sanitized)
	}
	if !strings.Contains(sanitized, "You can access files by path with this tool.") {
		t.Fatalf("expected neutral Read phrase in sanitized description: %q", sanitized)
	}
	if !strings.Contains(sanitized, "The file_path parameter must be an absolute path.") {
		t.Fatalf("sanitizer dropped unrelated description content: %q", sanitized)
	}
}
