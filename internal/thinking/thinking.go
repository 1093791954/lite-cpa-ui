package thinking

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type ThinkingMode int

const (
	ModeBudget ThinkingMode = iota
	ModeLevel
	ModeNone
	ModeAuto
)

type ThinkingLevel string

const (
	LevelNone    ThinkingLevel = "none"
	LevelAuto    ThinkingLevel = "auto"
	LevelMinimal ThinkingLevel = "minimal"
	LevelLow     ThinkingLevel = "low"
	LevelMedium  ThinkingLevel = "medium"
	LevelHigh    ThinkingLevel = "high"
	LevelXHigh   ThinkingLevel = "xhigh"
	LevelMax     ThinkingLevel = "max"
)

type SuffixResult struct {
	ModelName string
	HasSuffix bool
	RawSuffix string
}

// ParseSuffix extracts model(value) thinking suffix.
func ParseSuffix(model string) SuffixResult {
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen == -1 || !strings.HasSuffix(model, ")") {
		return SuffixResult{ModelName: model, HasSuffix: false}
	}
	return SuffixResult{
		ModelName: model[:lastOpen],
		HasSuffix: true,
		RawSuffix: model[lastOpen+1 : len(model)-1],
	}
}

var levelToBudgetMap = map[string]int{
	"none": 0, "auto": -1, "minimal": 512, "low": 1024,
	"medium": 8192, "high": 24576, "xhigh": 32768, "max": 128000,
}

func ConvertLevelToBudget(level string) (int, bool) {
	budget, ok := levelToBudgetMap[strings.ToLower(level)]
	return budget, ok
}

func ConvertBudgetToLevel(budget int) (string, bool) {
	switch {
	case budget < -1:
		return "", false
	case budget == -1:
		return string(LevelAuto), true
	case budget == 0:
		return string(LevelNone), true
	case budget <= 512:
		return string(LevelMinimal), true
	case budget <= 1024:
		return string(LevelLow), true
	case budget <= 8192:
		return string(LevelMedium), true
	case budget <= 24576:
		return string(LevelHigh), true
	default:
		return string(LevelXHigh), true
	}
}

func HasLevel(levels []string, target string) bool {
	for _, level := range levels {
		if strings.EqualFold(strings.TrimSpace(level), target) {
			return true
		}
	}
	return false
}

// MapToClaudeEffort maps a generic level to Claude adaptive effort.
// xhigh and max stay distinct; max falls back to high when unsupported.
func MapToClaudeEffort(level string, supportsMax bool) (string, bool) {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "":
		return "", false
	case "minimal":
		return "low", true
	case "low", "medium", "high", "xhigh":
		return level, true
	case "max":
		if supportsMax {
			return "max", true
		}
		return "high", true
	case "auto":
		return "high", true
	default:
		return "", false
	}
}

func GetThinkingText(part gjson.Result) string {
	if text := part.Get("text"); text.Exists() && text.Type == gjson.String {
		return text.String()
	}
	thinkingField := part.Get("thinking")
	if !thinkingField.Exists() {
		return ""
	}
	if thinkingField.Type == gjson.String {
		return thinkingField.String()
	}
	if thinkingField.IsObject() {
		if inner := thinkingField.Get("text"); inner.Exists() && inner.Type == gjson.String {
			return inner.String()
		}
		if inner := thinkingField.Get("thinking"); inner.Exists() && inner.Type == gjson.String {
			return inner.String()
		}
	}
	return ""
}

// ApplyThinking applies model(suffix) thinking config, then Claude outbound guards.
// Translators already map cross-format body fields; this only fills the suffix gap.
func ApplyThinking(body []byte, model string, fromFormat, toFormat, providerKey string) ([]byte, error) {
	_ = fromFormat
	_ = providerKey
	to := strings.ToLower(strings.TrimSpace(toFormat))
	if suffix := ParseSuffix(model); suffix.HasSuffix {
		body = applySuffix(body, suffix.RawSuffix, to)
	}
	if to == "claude" {
		body = sanitizeClaudeThinking(body)
	}
	return body, nil
}

func applySuffix(body []byte, raw, to string) []byte {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return body
	}
	switch to {
	case "claude":
		return applyClaudeSuffix(body, raw)
	case "openai":
		return applyOpenAISuffix(body, raw, "reasoning_effort")
	case "openai-response":
		return applyOpenAISuffix(body, raw, "reasoning.effort")
	default:
		return body
	}
}

func applyClaudeSuffix(body []byte, raw string) []byte {
	switch raw {
	case "none":
		return setClaudeDisabled(body)
	case "auto":
		body, _ = sjson.SetBytes(body, "thinking.type", "adaptive")
		body, _ = sjson.DeleteBytes(body, "thinking.budget_tokens")
		return clearClaudeEffort(body)
	case "minimal", "low", "medium", "high", "xhigh", "max":
		effort, ok := MapToClaudeEffort(raw, true)
		if !ok {
			return body
		}
		body, _ = sjson.SetBytes(body, "thinking.type", "adaptive")
		body, _ = sjson.DeleteBytes(body, "thinking.budget_tokens")
		body, _ = sjson.SetBytes(body, "output_config.effort", effort)
		return body
	}

	budget, err := strconv.Atoi(raw)
	if err != nil {
		return body
	}
	switch {
	case budget == 0:
		return setClaudeDisabled(body)
	case budget < 0:
		body, _ = sjson.SetBytes(body, "thinking.type", "adaptive")
		body, _ = sjson.DeleteBytes(body, "thinking.budget_tokens")
		return clearClaudeEffort(body)
	default:
		body, _ = sjson.SetBytes(body, "thinking.type", "enabled")
		body, _ = sjson.SetBytes(body, "thinking.budget_tokens", budget)
		return clearClaudeEffort(body)
	}
}

func applyOpenAISuffix(body []byte, raw, path string) []byte {
	switch raw {
	case "none", "auto", "minimal", "low", "medium", "high", "xhigh", "max":
		body, _ = sjson.SetBytes(body, path, raw)
		return body
	}
	budget, err := strconv.Atoi(raw)
	if err != nil {
		return body
	}
	if level, ok := ConvertBudgetToLevel(budget); ok {
		body, _ = sjson.SetBytes(body, path, level)
	}
	return body
}

func setClaudeDisabled(body []byte) []byte {
	body, _ = sjson.SetBytes(body, "thinking.type", "disabled")
	body, _ = sjson.DeleteBytes(body, "thinking.budget_tokens")
	return clearClaudeEffort(body)
}

func clearClaudeEffort(body []byte) []byte {
	body, _ = sjson.DeleteBytes(body, "output_config.effort")
	if oc := gjson.GetBytes(body, "output_config"); oc.Exists() && oc.IsObject() && len(oc.Map()) == 0 {
		body, _ = sjson.DeleteBytes(body, "output_config")
	}
	return body
}

// sanitizeClaudeThinking keeps Anthropic requests valid with adaptive/manual thinking.
func sanitizeClaudeThinking(body []byte) []byte {
	toolChoice := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "tool_choice.type").String()))
	if toolChoice == "any" || toolChoice == "tool" {
		body, _ = sjson.DeleteBytes(body, "thinking")
		return clearClaudeEffort(body)
	}

	thinkingType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()))
	switch thinkingType {
	case "enabled", "adaptive", "auto":
		if temp := gjson.GetBytes(body, "temperature"); temp.Exists() && !(temp.Type == gjson.Number && temp.Float() == 1) {
			body, _ = sjson.SetBytes(body, "temperature", 1)
		}
	}
	return body
}
