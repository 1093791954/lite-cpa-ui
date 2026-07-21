package thinking

import (
	"strings"

	"github.com/tidwall/gjson"
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

func MapToClaudeEffort(level string, supportsMax bool) (string, bool) {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "":
		return "", false
	case "minimal":
		return "low", true
	case "low", "medium", "high":
		return level, true
	case "xhigh", "max":
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

// ApplyThinking is a no-op for lite user-defined models.
// Translators already map thinking fields between formats.
func ApplyThinking(body []byte, model string, fromFormat, toFormat, providerKey string) ([]byte, error) {
	_ = model
	_ = fromFormat
	_ = toFormat
	_ = providerKey
	return body, nil
}
