package translator

import (
	"context"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Registry manages request/response transforms between formats.
type Registry struct {
	mu        sync.RWMutex
	requests  map[Format]map[Format]RequestTransform
	responses map[Format]map[Format]ResponseTransform
}

func New() *Registry {
	return &Registry{
		requests:  make(map[Format]map[Format]RequestTransform),
		responses: make(map[Format]map[Format]ResponseTransform),
	}
}

func (r *Registry) Register(from, to Format, request RequestTransform, response ResponseTransform) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.requests[from]; !ok {
		r.requests[from] = make(map[Format]RequestTransform)
	}
	if request != nil {
		r.requests[from][to] = request
	}
	if _, ok := r.responses[from]; !ok {
		r.responses[from] = make(map[Format]ResponseTransform)
	}
	r.responses[from][to] = response
}

// Register is the CPA-compatible package-level registration used by init files.
func Register(from, to string, request RequestTransform, response ResponseTransform) {
	Default().Register(FromString(from), FromString(to), request, response)
}

func (r *Registry) TranslateRequest(from, to Format, model string, rawJSON []byte, stream bool) []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if byTarget, ok := r.requests[from]; ok {
		if fn, ok := byTarget[to]; ok && fn != nil {
			return fn(model, rawJSON, stream)
		}
	}
	if model != "" && gjson.GetBytes(rawJSON, "model").String() != model {
		if updated, err := sjson.SetBytes(rawJSON, "model", model); err == nil {
			return updated
		}
	}
	return rawJSON
}

func (r *Registry) HasResponseTransformer(from, to Format) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if byTarget, ok := r.responses[from]; ok {
		_, ok := byTarget[to]
		return ok
	}
	return false
}

func (r *Registry) TranslateStream(ctx context.Context, from, to Format, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Response transformers are registered as (clientFormat -> upstreamFormat).
	// When translating upstream response back to client, look up responses[to][from]
	// where to=client source, from=upstream.
	if byTarget, ok := r.responses[to]; ok {
		if fn, ok := byTarget[from]; ok && fn.Stream != nil {
			return fn.Stream(ctx, model, originalRequestRawJSON, requestRawJSON, rawJSON, param)
		}
	}
	return [][]byte{rawJSON}
}

func (r *Registry) TranslateNonStream(ctx context.Context, from, to Format, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if byTarget, ok := r.responses[to]; ok {
		if fn, ok := byTarget[from]; ok && fn.NonStream != nil {
			return fn.NonStream(ctx, model, originalRequestRawJSON, requestRawJSON, rawJSON, param)
		}
	}
	return rawJSON
}

var defaultRegistry = New()

func Default() *Registry { return defaultRegistry }

func TranslateRequest(from, to Format, model string, rawJSON []byte, stream bool) []byte {
	return defaultRegistry.TranslateRequest(from, to, model, rawJSON, stream)
}

func TranslateStream(ctx context.Context, from, to Format, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	return defaultRegistry.TranslateStream(ctx, from, to, model, originalRequestRawJSON, requestRawJSON, rawJSON, param)
}

func TranslateNonStream(ctx context.Context, from, to Format, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte {
	return defaultRegistry.TranslateNonStream(ctx, from, to, model, originalRequestRawJSON, requestRawJSON, rawJSON, param)
}

func HasResponseTransformer(from, to Format) bool {
	return defaultRegistry.HasResponseTransformer(from, to)
}
