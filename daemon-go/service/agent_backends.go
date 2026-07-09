package service

import (
	"fmt"
	"strings"

	"github.com/repowire/repowire/daemon-go/proto"
)

type backendResumeSpec struct {
	supported  bool
	strategy   string
	flag       string
	subcommand string
}

var backendResumeSpecs = map[proto.AgentType]backendResumeSpec{
	proto.AgentClaudeCode:  {supported: true, strategy: "claude_resume", flag: "--resume"},
	proto.AgentCodex:       {supported: true, strategy: "codex_resume", subcommand: "resume"},
	proto.AgentGemini:      {supported: true, strategy: "gemini_resume", flag: "--resume"},
	proto.AgentOpenCode:    {supported: true, strategy: "opencode_session", flag: "--session"},
	proto.AgentAntigravity: {supported: true, strategy: "antigravity_conversation", flag: "--conversation"},
	proto.AgentPi:          {supported: true, strategy: "pi_session", flag: "--session"},
	proto.AgentMCPHTTP:     {supported: false, strategy: "unsupported"},
}

// ResumeCapabilityForRegistration mirrors agent_backends.resume_capability_for_registration.
func ResumeCapabilityForRegistration(backend proto.AgentType, runtimeSessionID string) map[string]any {
	if runtimeSessionID == "" {
		return map[string]any{}
	}
	spec := backendResumeSpecs[backend]
	if !spec.supported {
		return map[string]any{
			"supported": false,
			"strategy":  spec.strategy,
			"reason":    "backend_resume_not_implemented",
		}
	}
	return map[string]any{
		"supported":              true,
		"strategy":               spec.strategy,
		"runtime_session_id_arg": runtimeSessionID,
	}
}

func canResumeBackend(backend proto.AgentType, runtimeSessionID string) bool {
	spec := backendResumeSpecs[backend]
	return spec.supported && runtimeSessionID != ""
}

// BuildResumeCommand appends the backend-native resume argument to a base launch command.
func BuildResumeCommand(command string, backend proto.AgentType, runtimeSessionID string) (string, error) {
	spec := backendResumeSpecs[backend]
	if !spec.supported {
		return "", fmt.Errorf("backend-native resume is not available for %s", backend)
	}
	if runtimeSessionID == "" {
		return "", fmt.Errorf("runtime_session_id is required for backend resume")
	}
	arg := resumeShellQuote(runtimeSessionID)
	if spec.subcommand != "" {
		return strings.TrimSpace(command) + " " + spec.subcommand + " " + arg, nil
	}
	return strings.TrimSpace(command) + " " + spec.flag + " " + arg, nil
}

// ResumeCommand builds the launch command from the resume_plan_info map recorded
// by the shared resolver. It intentionally accepts the untyped map because
// SessionControl persists that map verbatim in operation attempts.
func ResumeCommand(command string, backend proto.AgentType, plan map[string]any) (string, error) {
	if plan == nil {
		return command, nil
	}
	if pb, _ := plan["backend"].(string); pb != "" && pb != string(backend) {
		return "", fmt.Errorf("resume backend mismatch: requested %s, plan %s", backend, pb)
	}
	runtimeSessionID, _ := plan["runtime_session_id"].(string)
	return BuildResumeCommand(command, backend, runtimeSessionID)
}

func resumeShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' ||
			r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '@' || r == '%') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
