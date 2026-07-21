package translator

import (
	chatclaude "github.com/Mieluoxxx/lite-cpa/internal/translator/chat/claude"
	chatresp "github.com/Mieluoxxx/lite-cpa/internal/translator/chat/responses"
	claudechat "github.com/Mieluoxxx/lite-cpa/internal/translator/claude/chat"
	clauderesp "github.com/Mieluoxxx/lite-cpa/internal/translator/claude/responses"
	respchat "github.com/Mieluoxxx/lite-cpa/internal/translator/responses/chat"
	respclaude "github.com/Mieluoxxx/lite-cpa/internal/translator/responses/claude"
)

// RegisterBuiltin registers conversion among chat / responses / claude.
// Cross-format packages live at internal/translator/{from}/{to}/.
// Same-format identity converters live in this package.
func RegisterBuiltin() {
	// chat → chat
	Register(OpenAI, OpenAI,
		ConvertOpenAIRequestToOpenAI,
		ResponseTransform{
			Stream:    ConvertOpenAIResponseToOpenAI,
			NonStream: ConvertOpenAIResponseToOpenAINonStream,
		},
	)

	// responses → chat
	Register(OpenaiResponse, OpenAI,
		respchat.ConvertOpenAIResponsesRequestToOpenAIChatCompletions,
		ResponseTransform{
			Stream:    chatresp.ConvertOpenAIChatCompletionsResponseToOpenAIResponses,
			NonStream: chatresp.ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream,
		},
	)

	// chat → responses
	Register(OpenAI, OpenaiResponse,
		chatresp.ConvertOpenAIChatCompletionsRequestToOpenAIResponses,
		ResponseTransform{
			Stream:    respchat.ConvertOpenAIResponsesResponseToOpenAIChatCompletions,
			NonStream: respchat.ConvertOpenAIResponsesResponseToOpenAIChatCompletionsNonStream,
		},
	)

	// responses → responses
	Register(OpenaiResponse, OpenaiResponse,
		ConvertOpenAIResponsesRequestIdentity,
		ResponseTransform{
			Stream:    ConvertOpenAIResponsesResponseIdentityStream,
			NonStream: ConvertOpenAIResponsesResponseIdentityNonStream,
		},
	)

	// chat → claude
	Register(OpenAI, Claude,
		chatclaude.ConvertOpenAIRequestToClaude,
		ResponseTransform{
			Stream:    claudechat.ConvertClaudeResponseToOpenAI,
			NonStream: claudechat.ConvertClaudeResponseToOpenAINonStream,
		},
	)

	// claude → chat
	Register(Claude, OpenAI,
		claudechat.ConvertClaudeRequestToOpenAI,
		ResponseTransform{
			Stream:     chatclaude.ConvertOpenAIResponseToClaude,
			NonStream:  chatclaude.ConvertOpenAIResponseToClaudeNonStream,
			TokenCount: chatclaude.ClaudeTokenCount,
		},
	)

	// responses → claude
	Register(OpenaiResponse, Claude,
		respclaude.ConvertOpenAIResponsesRequestToClaude,
		ResponseTransform{
			Stream:    clauderesp.ConvertClaudeResponseToOpenAIResponses,
			NonStream: clauderesp.ConvertClaudeResponseToOpenAIResponsesNonStream,
		},
	)

	// claude → responses
	Register(Claude, OpenaiResponse,
		clauderesp.ConvertClaudeRequestToOpenAIResponses,
		ResponseTransform{
			Stream:    respclaude.ConvertOpenAIResponsesResponseToClaude,
			NonStream: respclaude.ConvertOpenAIResponsesResponseToClaudeNonStream,
		},
	)
}
