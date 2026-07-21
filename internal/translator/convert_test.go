package translator_test

import (
	"context"
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
