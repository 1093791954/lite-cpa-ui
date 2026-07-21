package translator

import (
	"bytes"
	"context"

	"github.com/tidwall/sjson"
)

// ConvertOpenAIResponsesRequestIdentity normalizes model/stream for same-format hop.
func ConvertOpenAIResponsesRequestIdentity(modelName string, inputRawJSON []byte, stream bool) []byte {
	out := inputRawJSON
	if modelName != "" {
		out, _ = sjson.SetBytes(out, "model", modelName)
	}
	out, _ = sjson.SetBytes(out, "stream", stream)
	return out
}

// ConvertOpenAIResponsesResponseIdentityStream forwards Responses SSE data lines.
func ConvertOpenAIResponsesResponseIdentityStream(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) [][]byte {
	line := bytes.TrimSpace(rawJSON)
	if len(line) == 0 {
		return nil
	}
	if bytes.HasPrefix(line, []byte("event:")) {
		// Preserve event lines as-is with trailing structure handled by forwarder.
		return [][]byte{append(bytes.Clone(line), '\n')}
	}
	if bytes.HasPrefix(line, []byte("data:")) {
		return [][]byte{append(bytes.Clone(line), '\n')}
	}
	// Bare JSON payload → data line
	out := make([]byte, 0, len(line)+8)
	out = append(out, "data: "...)
	out = append(out, line...)
	out = append(out, '\n')
	return [][]byte{out}
}

func ConvertOpenAIResponsesResponseIdentityNonStream(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) []byte {
	return rawJSON
}
