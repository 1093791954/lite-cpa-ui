package util

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
)

var (
	claudeToolUseIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	claudeToolUseIDCounter   uint64
)

func SanitizeClaudeToolID(id string) string {
	s := claudeToolUseIDSanitizer.ReplaceAllString(id, "_")
	if s == "" {
		s = fmt.Sprintf("toolu_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&claudeToolUseIDCounter, 1))
	}
	return s
}

func CanonicalToolName(name string) string {
	canonical := strings.TrimSpace(name)
	canonical = strings.TrimLeft(canonical, "_")
	return strings.ToLower(canonical)
}

func ToolNameMapFromClaudeRequest(rawJSON []byte) map[string]string {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return nil
	}
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return nil
	}
	out := make(map[string]string)
	tools.ForEach(func(_, tool gjson.Result) bool {
		name := strings.TrimSpace(tool.Get("name").String())
		if name == "" {
			name = strings.TrimSpace(tool.Get("function.name").String())
		}
		if name == "" {
			return true
		}
		key := CanonicalToolName(name)
		if key == "" {
			return true
		}
		if _, exists := out[key]; !exists {
			out[key] = name
		}
		return true
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

func MapToolName(toolNameMap map[string]string, name string) string {
	if name == "" || toolNameMap == nil {
		return name
	}
	if mapped, ok := toolNameMap[CanonicalToolName(name)]; ok && mapped != "" {
		return mapped
	}
	return name
}

// FixJSON converts single-quoted JSON-ish strings to valid JSON.
func FixJSON(input string) string {
	var out bytes.Buffer
	inDouble := false
	inSingle := false
	escaped := false
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inDouble {
			out.WriteRune(r)
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				inDouble = false
			}
			continue
		}
		if inSingle {
			if escaped {
				escaped = false
				switch r {
				case 'n', 'r', 't', 'b', 'f', '/', '"':
					out.WriteByte('\\')
					out.WriteRune(r)
				case '\\':
					out.WriteByte('\\')
					out.WriteByte('\\')
				case '\'':
					out.WriteRune('\'')
				case 'u':
					out.WriteByte('\\')
					out.WriteByte('u')
					for k := 0; k < 4 && i+1 < len(runes); k++ {
						peek := runes[i+1]
						if (peek >= '0' && peek <= '9') || (peek >= 'a' && peek <= 'f') || (peek >= 'A' && peek <= 'F') {
							out.WriteRune(peek)
							i++
						} else {
							break
						}
					}
				default:
					out.WriteByte('\\')
					out.WriteRune(r)
				}
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '\'' {
				out.WriteByte('"')
				inSingle = false
				continue
			}
			if r == '"' {
				out.WriteByte('\\')
				out.WriteByte('"')
				continue
			}
			out.WriteRune(r)
			continue
		}
		if r == '"' {
			inDouble = true
			out.WriteRune(r)
			continue
		}
		if r == '\'' {
			inSingle = true
			out.WriteByte('"')
			continue
		}
		out.WriteRune(r)
	}
	if inSingle {
		out.WriteByte('"')
	}
	return out.String()
}
