package server

import "testing"

func TestTokenUsageFromResponse(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		input   int64
		output  int64
	}{
		{
			name:    "openai non-stream response",
			payload: []byte(`{"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150}}`),
			input:   120,
			output:  30,
		},
		{
			name:    "anthropic stream usage across events",
			payload: []byte("event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":100,\"cache_creation_input_tokens\":20,\"cache_read_input_tokens\":30}}}\n\nevent: message_delta\ndata: {\"usage\":{\"output_tokens\":40}}\n\n"),
			input:   150,
			output:  40,
		},
		{
			name:    "responses completed event",
			payload: []byte("event: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":70,\"output_tokens\":25}}}\n\n"),
			input:   70,
			output:  25,
		},
		{
			name:    "terminal event without usage",
			payload: []byte("data: [DONE]\n\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := usageFromResponse(tt.payload)
			if got.inputTokens != tt.input || got.outputTokens != tt.output {
				t.Fatalf("usage = (%d, %d), want (%d, %d)", got.inputTokens, got.outputTokens, tt.input, tt.output)
			}
		})
	}
}

func TestTokenUsageMergePayload(t *testing.T) {
	var usage tokenUsage
	usage.mergePayload([]byte("data: {\"message\":{\"usage\":{\"input_tokens\":64}}}\n\n"))
	usage.mergePayload([]byte("data: {\"usage\":{\"output_tokens\":16}}\n\n"))

	if !usage.inputSeen || !usage.outputSeen {
		t.Fatalf("usage visibility input=%t output=%t, want both true", usage.inputSeen, usage.outputSeen)
	}
	if usage.inputTokens != 64 || usage.outputTokens != 16 {
		t.Fatalf("usage = (%d, %d), want (64, 16)", usage.inputTokens, usage.outputTokens)
	}
}
