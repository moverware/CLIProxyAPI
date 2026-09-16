package signature

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestSanitizeClaudeMessagesPreservesSystemTurnBoundary(t *testing.T) {
	const system = `{"role":"system","content":[{"type":"text","text":"Only you see that command output; summarize it for the user."}],"clear_at":"next_user_message"}`
	const stringSystem = `{"role":"system","content":"Only you see that command output; summarize it for the user."}`
	const removed = `{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":""}]}`
	const user = `{"role":"user","content":[{"type":"text","text":"The user named this session auto review."}]}`
	const tool = `{"role":"assistant","content":[{"type":"tool_use","id":"toolu_test","name":"Bash","input":{"command":"pwd"}}]}`
	const result = `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_test","content":"/tmp"}]}`
	const directive = `{"role":"system","content":[],"output_config":{"effort":"high"}}`
	const toolSystem = `{"role":"system","content":[],"tool_additions":[{"name":"Bash","input_schema":{"type":"object"}}],"tool_removals":["Read"]}`
	const toolDirective = `{"role":"system","content":[],"output_config":{"effort":"high"},"tool_removals":["Read"]}`
	const placeholder = `{"role":"assistant","content":[{"type":"text","text":"[Thinking omitted]"}]}`

	for _, test := range []struct {
		name  string
		input []string
		want  []string
	}{
		{"native rename interrupts thinking", []string{system, removed, user, tool, result}, []string{system, placeholder, user, tool, result}},
		{"string system before interrupted thinking", []string{stringSystem, removed, user, tool, result}, []string{stringSystem, placeholder, user, tool, result}},
		{"terminal system needs no assistant", []string{user, system, removed}, []string{user, system}},
		{"surviving assistant supplies boundary", []string{system, removed, tool, result}, []string{system, tool, result}},
		{"consecutive removed assistants", []string{system, removed, removed, user}, []string{system, placeholder, user}},
		{"successive system instructions", []string{system, removed, toolSystem, removed, user}, []string{system, placeholder, toolSystem, placeholder, user}},
		{"directive allowed before user", []string{directive, removed, user}, []string{directive, user}},
		{"tool changes require assistant despite directive", []string{toolDirective, removed, user}, []string{toolDirective, placeholder, user}},
		{"preserve directive following system", []string{system, removed, directive, user}, []string{system, placeholder, directive, user}},
		{"ordinary thinking turn removed", []string{user, removed, user}, []string{user, user}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(`{"messages":[` + strings.Join(test.input, ",") + `]}`)
			output, _ := SanitizeClaudeMessagesForClaudeUpstream(input, "claude-fable-5-1")
			messages := gjson.GetBytes(output, "messages").Array()
			if len(messages) != len(test.want) {
				t.Fatalf("got %d messages, want %d: %s", len(messages), len(test.want), output)
			}
			for i, want := range test.want {
				if messages[i].Raw != want {
					t.Fatalf("message %d = %s, want %s", i, messages[i].Raw, want)
				}
			}
			if second, _ := SanitizeClaudeMessagesForClaudeUpstream(output, "claude-fable-5-1"); string(second) != string(output) {
				t.Fatalf("sanitization is not idempotent: %s", second)
			}
		})
	}
}

func TestSanitizeClaudeMessagesLeavesValidSystemHistoryUnchanged(t *testing.T) {
	input := []byte(`{"messages":[{"role":"system","content":[{"type":"text","text":"Keep this instruction."}]},{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning","signature":"` + testClaudeThinkingSignature() + `"}]},{"role":"user","content":"continue"},{"role":"system","content":[{"type":"text","text":"Last instruction."}]}]}`)
	output, report := SanitizeClaudeMessagesForClaudeUpstream(input, "claude-sonnet-4-6")
	if string(output) != string(input) || report.DroppedBlocks != 0 {
		t.Fatalf("valid history changed: %s (%+v)", output, report)
	}
}
