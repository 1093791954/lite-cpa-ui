package translator

import "context"

// RequestTransform converts a request payload from source schema to target schema.
type RequestTransform func(model string, rawJSON []byte, stream bool) []byte

// ResponseStreamTransform converts a streaming response chunk.
type ResponseStreamTransform func(ctx context.Context, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte

// ResponseNonStreamTransform converts a non-streaming response body.
type ResponseNonStreamTransform func(ctx context.Context, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte

// ResponseTokenCountTransform converts a token count response.
type ResponseTokenCountTransform func(ctx context.Context, count int64) []byte

// ResponseTransform groups response converters.
type ResponseTransform struct {
	Stream     ResponseStreamTransform
	NonStream  ResponseNonStreamTransform
	TokenCount ResponseTokenCountTransform
}

// Compatibility aliases used by ported CPA translator packages.
type TranslateRequestFunc = RequestTransform
type TranslateResponseFunc = ResponseStreamTransform
type TranslateResponseNonStreamFunc = ResponseNonStreamTransform
type TranslateResponse = ResponseTransform
