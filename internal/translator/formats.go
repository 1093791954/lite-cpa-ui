package translator

// Format identifies a request/response schema.
type Format string

const (
	FormatOpenAI         Format = "openai"
	FormatOpenAIResponse Format = "openai-response"
	FormatClaude         Format = "claude"
)

// String constants used by ported CPA translators and init registration.
const (
	OpenAI         = "openai"
	OpenaiResponse = "openai-response"
	Claude         = "claude"
)

func FromString(v string) Format { return Format(v) }

func (f Format) String() string { return string(f) }
