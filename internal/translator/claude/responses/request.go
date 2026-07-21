package responses

import (
	claudechat "github.com/Mieluoxxx/lite-cpa/internal/translator/claude/chat"
	chatresp "github.com/Mieluoxxx/lite-cpa/internal/translator/chat/responses"
)

// ConvertClaudeRequestToOpenAIResponses converts Claude Messages → standard OpenAI Responses
// via Claude→Chat then Chat→Responses (two-hop).
func ConvertClaudeRequestToOpenAIResponses(modelName string, inputRawJSON []byte, stream bool) []byte {
	chat := claudechat.ConvertClaudeRequestToOpenAI(modelName, inputRawJSON, stream)
	return chatresp.ConvertOpenAIChatCompletionsRequestToOpenAIResponses(modelName, chat, stream)
}
