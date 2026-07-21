package translator_test

import (
	"context"
	"os"
	"testing"

	"github.com/Mieluoxxx/lite-cpa/internal/translator"
	"github.com/tidwall/gjson"
)

func TestClaudeSSENonStreamToOpenAI(t *testing.T) {
	raw, err := os.ReadFile("/tmp/anth_sse.txt")
	if err != nil {
		// embed inline
		raw = []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello-from-claude\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n")
	}
	out := translator.TranslateNonStream(context.Background(), translator.FormatClaude, translator.FormatOpenAI, "claude-sonnet-4", nil, nil, raw, new(any))
	if gjson.GetBytes(out, "choices.0.message.content").String() != "hello-from-claude" {
		t.Fatalf("chat: %s", out)
	}
	out2 := translator.TranslateNonStream(context.Background(), translator.FormatClaude, translator.FormatOpenAIResponse, "claude-sonnet-4", nil, nil, raw, new(any))
	if gjson.GetBytes(out2, "object").String() != "response" {
		t.Fatalf("responses: %s", out2)
	}
}
