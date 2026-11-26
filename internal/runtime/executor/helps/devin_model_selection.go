package helps

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
)

// DevinModelOptions keeps the requested service tier separate from reasoning effort.
type DevinModelOptions struct {
	ThinkingLevel string
	BudgetTokens  int
	ServiceTier   string
}

type devinModelError struct{ message string }

func (e devinModelError) Error() string       { return e.message }
func (devinModelError) StatusCode() int       { return http.StatusBadRequest }
func (devinModelError) IsRequestScoped() bool { return true }

// ResolveDevinRequestModelUID selects GPT priority variants without changing other providers' UIDs.
func ResolveDevinRequestModelUID(model string, opts DevinModelOptions) (string, error) {
	clean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "devin/")
	parsed := thinking.ParseSuffix(clean)
	base := parsed.ModelName
	if !parsed.HasSuffix {
		if before, after, ok := strings.Cut(base, ":"); ok {
			base, parsed.RawSuffix, parsed.HasSuffix = before, after, true
		}
	}
	base = strings.ReplaceAll(strings.TrimSpace(base), ".", "-")
	if !strings.HasPrefix(base, "gpt-") {
		return ResolveDevinChatModelUID(model, opts.ThinkingLevel, opts.BudgetTokens), nil
	}

	tier := strings.ToLower(strings.TrimSpace(opts.ServiceTier))
	switch tier {
	case "", "auto", "default", "priority":
	default:
		return "", devinModelError{message: fmt.Sprintf("unsupported Devin service_tier %q", opts.ServiceTier)}
	}
	level, fast := devinFastEffort(opts.ThinkingLevel)
	fast = fast || tier == "priority"
	if parsed.HasSuffix {
		suffixLevel, suffixFast := devinFastEffort(parsed.RawSuffix)
		fast = fast || suffixFast
		if !suffixFast || suffixLevel != "" {
			level = suffixLevel
		}
	}
	for _, suffix := range []string{"-priority", "-fast"} {
		if before, ok := strings.CutSuffix(base, suffix); ok {
			base, fast = before, true
			break
		}
	}
	if !fast {
		return ResolveDevinChatModelUID(model, opts.ThinkingLevel, opts.BudgetTokens), nil
	}

	family := base
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if before, ok := strings.CutSuffix(family, "-"+effort); ok {
			family, level = before, effort
			break
		}
	}
	switch family {
	case "gpt-6-2-astra", "gpt-6-2-sol":
		return ResolveDevinChatModelUID(family, level, opts.BudgetTokens) + "-priority", nil
	default:
		return "", devinModelError{message: fmt.Sprintf("Devin Fast mode is not supported for model %q", model)}
	}
}

func devinFastEffort(level string) (string, bool) {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "fast" {
		return "", true
	}
	if effort, ok := strings.CutSuffix(level, "-fast"); ok {
		if _, parsed := thinking.ParseLevelSuffix(strings.TrimSpace(effort)); parsed || effort == "none" {
			return effort, true
		}
	}
	return level, false
}
