package translator_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Mieluoxxx/lite-cpa/internal/translator"
	"github.com/tidwall/gjson"
)

func TestMain(m *testing.M) {
	translator.RegisterBuiltin()
	m.Run()
}

func TestChatToClaudeRequest(t *testing.T) {
	in := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	out := translator.TranslateRequest(translator.FormatOpenAI, translator.FormatClaude, "claude-sonnet-4", in, false)
	if gjson.GetBytes(out, "model").String() != "claude-sonnet-4" {
		t.Fatalf("model: %s", out)
	}
	if !gjson.GetBytes(out, "messages").IsArray() {
		t.Fatalf("messages missing: %s", out)
	}
	if gjson.GetBytes(out, "messages.0.role").String() != "user" {
		t.Fatalf("role: %s", out)
	}
}

func TestChatToResponsesRequest(t *testing.T) {
	in := []byte(`{"model":"gpt-5","messages":[{"role":"system","content":"sys"},{"role":"user","content":"hi"}],"max_tokens":100,"temperature":0.2}`)
	out := translator.TranslateRequest(translator.FormatOpenAI, translator.FormatOpenAIResponse, "gpt-5", in, false)
	if gjson.GetBytes(out, "model").String() != "gpt-5" {
		t.Fatalf("model: %s", out)
	}
	if gjson.GetBytes(out, "instructions").String() != "sys" {
		t.Fatalf("instructions: %s", out)
	}
	if gjson.GetBytes(out, "max_output_tokens").Int() != 100 {
		t.Fatalf("max_output_tokens: %s", out)
	}
	if gjson.GetBytes(out, "temperature").Float() != 0.2 {
		t.Fatalf("temperature must be preserved (not Codex-stripped): %s", out)
	}
	if !gjson.GetBytes(out, "input").IsArray() {
		t.Fatalf("input missing: %s", out)
	}
}

func TestResponsesIdentity(t *testing.T) {
	in := []byte(`{"model":"gpt-5","input":"hello","max_output_tokens":50}`)
	out := translator.TranslateRequest(translator.FormatOpenAIResponse, translator.FormatOpenAIResponse, "gpt-5", in, true)
	if gjson.GetBytes(out, "stream").Bool() != true {
		t.Fatalf("stream: %s", out)
	}
	if gjson.GetBytes(out, "max_output_tokens").Int() != 50 {
		t.Fatalf("max_output_tokens dropped: %s", out)
	}
}

func TestResponsesToChatNonStream(t *testing.T) {
	resp := []byte(`{"id":"resp_1","object":"response","model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`)
	out := translator.TranslateNonStream(context.Background(), translator.FormatOpenAIResponse, translator.FormatOpenAI, "gpt-5", nil, nil, resp, new(any))
	if gjson.GetBytes(out, "object").String() != "chat.completion" {
		t.Fatalf("object: %s", out)
	}
	if gjson.GetBytes(out, "choices.0.message.content").String() != "hello" {
		t.Fatalf("content: %s", out)
	}
}

// Official Responses streams key function_call by call_id on output_item.added,
// then send function_call_arguments.delta with item_id only (item.id / fc_*).
// Those must accumulate onto the same chat tool_call index — otherwise clients
// receive name without arguments ("tool input was not fully received").
func TestResponsesToChatStreamToolCallItemID(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5","created_at":1}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_123","type":"function_call","call_id":"call_abc","name":"Read","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_123","output_index":0,"delta":"{\"path\":"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_123","output_index":0,"delta":"\"conversation.md\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_123","output_index":0,"arguments":"{\"path\":\"conversation.md\"}"}`,
		`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
	}

	var param any
	var args strings.Builder
	var sawName, sawID, finish string
	for _, ev := range events {
		chunks := translator.TranslateStream(context.Background(), translator.FormatOpenAIResponse, translator.FormatOpenAI, "gpt-5", nil, nil, []byte(ev), &param)
		for _, chunk := range chunks {
			line := bytes.TrimSpace(chunk)
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			payload := bytes.TrimSpace(line[5:])
			if bytes.Equal(payload, []byte("[DONE]")) {
				continue
			}
			root := gjson.ParseBytes(payload)
			if id := root.Get("choices.0.delta.tool_calls.0.id").String(); id != "" {
				sawID = id
			}
			if name := root.Get("choices.0.delta.tool_calls.0.function.name").String(); name != "" {
				sawName = name
			}
			if a := root.Get("choices.0.delta.tool_calls.0.function.arguments").String(); a != "" {
				args.WriteString(a)
			}
			if fr := root.Get("choices.0.finish_reason").String(); fr != "" && fr != "null" {
				finish = fr
			}
			// Guard: deltas must not invent a second tool_call index.
			if idx := root.Get("choices.0.delta.tool_calls.0.index"); idx.Exists() && idx.Int() != 0 {
				t.Fatalf("unexpected tool index %d in %s", idx.Int(), payload)
			}
		}
	}

	if sawID != "call_abc" {
		t.Fatalf("tool id: got %q want call_abc", sawID)
	}
	if sawName != "Read" {
		t.Fatalf("tool name: got %q want Read", sawName)
	}
	if args.String() != `{"path":"conversation.md"}` {
		t.Fatalf("arguments: got %q", args.String())
	}
	if finish != "tool_calls" {
		t.Fatalf("finish_reason: got %q want tool_calls", finish)
	}
}
