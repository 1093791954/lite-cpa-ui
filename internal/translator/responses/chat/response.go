package chat

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type responsesToChatState struct {
	ID               string
	Model            string
	Created          int64
	Content          strings.Builder
	Reasoning        strings.Builder
	ToolCalls        map[string]*toolCallAcc // call_id and item_id -> acc
	ToolByOutputIdx  map[int]*toolCallAcc
	ToolOrder        []string // primary chat tool-call ids
	FinishReason     string
	PromptTokens     int64
	CachedTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

type toolCallAcc struct {
	ID        string // chat-facing id (Responses call_id)
	ItemID    string // Responses item.id (fc_*)
	Name      string
	Arguments strings.Builder
	Index     int
}

// ConvertOpenAIResponsesResponseToOpenAIChatCompletions converts Responses SSE
// events into OpenAI chat.completion.chunk SSE lines.
func ConvertOpenAIResponsesResponseToOpenAIChatCompletions(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	_ = ctx
	_ = originalRequestRawJSON
	_ = requestRawJSON
	if *param == nil {
		*param = &responsesToChatState{
			Model:           modelName,
			Created:         time.Now().Unix(),
			ToolCalls:       make(map[string]*toolCallAcc),
			ToolByOutputIdx: make(map[int]*toolCallAcc),
		}
	}
	st := (*param).(*responsesToChatState)

	line := bytes.TrimSpace(rawJSON)
	if len(line) == 0 {
		return nil
	}
	// Accept both "event:"/"data:" pairs and bare data lines.
	if bytes.HasPrefix(line, []byte("event:")) {
		return nil
	}
	if bytes.HasPrefix(line, []byte("data:")) {
		line = bytes.TrimSpace(line[5:])
	}
	if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
		if bytes.Equal(line, []byte("[DONE]")) {
			return [][]byte{[]byte("data: [DONE]")}
		}
		return nil
	}

	root := gjson.ParseBytes(line)
	typ := root.Get("type").String()
	if typ == "" {
		// Non-stream full response object.
		if root.Get("object").String() == "response" || root.Get("output").Exists() {
			return [][]byte{[]byte("data: " + string(responsesObjectToChatCompletion(root, modelName))), []byte("data: [DONE]")}
		}
		return nil
	}

	var out [][]byte
	emitChunk := func(delta []byte, finish string) {
		chunk := []byte(`{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{},"finish_reason":null}]}`)
		id := st.ID
		if id == "" {
			id = "chatcmpl-lite"
		}
		chunk, _ = sjson.SetBytes(chunk, "id", id)
		chunk, _ = sjson.SetBytes(chunk, "created", st.Created)
		chunk, _ = sjson.SetBytes(chunk, "model", firstNonEmpty(st.Model, modelName))
		if len(delta) > 0 {
			chunk, _ = sjson.SetRawBytes(chunk, "choices.0.delta", delta)
		}
		if finish != "" {
			chunk, _ = sjson.SetBytes(chunk, "choices.0.finish_reason", finish)
		}
		out = append(out, append([]byte("data: "), chunk...))
	}

	switch typ {
	case "response.created", "response.in_progress":
		if id := root.Get("response.id").String(); id != "" {
			st.ID = id
		}
		if m := root.Get("response.model").String(); m != "" {
			st.Model = m
		}
		if c := root.Get("response.created_at").Int(); c > 0 {
			st.Created = c
		}
		// role chunk
		emitChunk([]byte(`{"role":"assistant"}`), "")
	case "response.output_text.delta":
		delta := root.Get("delta").String()
		if delta == "" {
			delta = root.Get("text").String()
		}
		if delta != "" {
			st.Content.WriteString(delta)
			d, _ := sjson.SetBytes([]byte(`{}`), "content", delta)
			emitChunk(d, "")
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		delta := root.Get("delta").String()
		if delta != "" {
			st.Reasoning.WriteString(delta)
			d, _ := sjson.SetBytes([]byte(`{}`), "reasoning_content", delta)
			emitChunk(d, "")
		}
	case "response.output_item.added":
		item := root.Get("item")
		if item.Get("type").String() == "function_call" {
			itemID := item.Get("id").String()
			callID := item.Get("call_id").String()
			if callID == "" {
				callID = itemID
			}
			name := item.Get("name").String()
			acc := st.ensureToolCall(callID, itemID, name, root.Get("output_index"))
			tc := []byte(`{"tool_calls":[{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}]}`)
			tc, _ = sjson.SetBytes(tc, "tool_calls.0.index", acc.Index)
			tc, _ = sjson.SetBytes(tc, "tool_calls.0.id", acc.ID)
			tc, _ = sjson.SetBytes(tc, "tool_calls.0.function.name", acc.Name)
			// Some providers put the full JSON on the item already.
			if args := item.Get("arguments").String(); args != "" {
				acc.Arguments.WriteString(args)
				tc, _ = sjson.SetBytes(tc, "tool_calls.0.function.arguments", args)
			}
			emitChunk(tc, "")
		}
	case "response.function_call_arguments.delta":
		delta := root.Get("delta").String()
		if delta == "" {
			break
		}
		acc := st.lookupToolCall(root.Get("call_id").String(), root.Get("item_id").String(), root.Get("output_index"))
		if acc == nil {
			// Last resort: keep stream alive without inventing a second tool index
			// when we can still bind by output_index later via done.
			acc = st.ensureToolCall(root.Get("call_id").String(), root.Get("item_id").String(), "", root.Get("output_index"))
		}
		acc.Arguments.WriteString(delta)
		tc := []byte(`{"tool_calls":[{"index":0,"function":{"arguments":""}}]}`)
		tc, _ = sjson.SetBytes(tc, "tool_calls.0.index", acc.Index)
		tc, _ = sjson.SetBytes(tc, "tool_calls.0.function.arguments", delta)
		emitChunk(tc, "")
	case "response.function_call_arguments.done":
		// Official recovery path when deltas used item_id only or were skipped.
		args := root.Get("arguments").String()
		acc := st.lookupToolCall(root.Get("call_id").String(), root.Get("item_id").String(), root.Get("output_index"))
		if acc == nil {
			acc = st.ensureToolCall(root.Get("call_id").String(), root.Get("item_id").String(), "", root.Get("output_index"))
		}
		if args != "" && acc.Arguments.Len() == 0 {
			acc.Arguments.WriteString(args)
			tc := []byte(`{"tool_calls":[{"index":0,"function":{"arguments":""}}]}`)
			tc, _ = sjson.SetBytes(tc, "tool_calls.0.index", acc.Index)
			tc, _ = sjson.SetBytes(tc, "tool_calls.0.function.arguments", args)
			emitChunk(tc, "")
		}
	case "response.output_item.done":
		item := root.Get("item")
		if item.Get("type").String() != "function_call" {
			break
		}
		itemID := item.Get("id").String()
		callID := item.Get("call_id").String()
		if callID == "" {
			callID = itemID
		}
		name := item.Get("name").String()
		args := item.Get("arguments").String()
		acc := st.lookupToolCall(callID, itemID, root.Get("output_index"))
		if acc == nil {
			acc = st.ensureToolCall(callID, itemID, name, root.Get("output_index"))
		} else if name != "" && acc.Name == "" {
			acc.Name = name
		}
		if args != "" && acc.Arguments.Len() == 0 {
			acc.Arguments.WriteString(args)
			tc := []byte(`{"tool_calls":[{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}]}`)
			tc, _ = sjson.SetBytes(tc, "tool_calls.0.index", acc.Index)
			tc, _ = sjson.SetBytes(tc, "tool_calls.0.id", acc.ID)
			if acc.Name != "" {
				tc, _ = sjson.SetBytes(tc, "tool_calls.0.function.name", acc.Name)
			}
			tc, _ = sjson.SetBytes(tc, "tool_calls.0.function.arguments", args)
			emitChunk(tc, "")
		}
	case "response.completed", "response.incomplete":
		if usage := root.Get("response.usage"); usage.Exists() {
			st.PromptTokens = usage.Get("input_tokens").Int()
			st.CachedTokens = usage.Get("input_tokens_details.cached_tokens").Int()
			st.CompletionTokens = usage.Get("output_tokens").Int()
			st.TotalTokens = usage.Get("total_tokens").Int()
		}
		finish := "stop"
		if len(st.ToolOrder) > 0 {
			finish = "tool_calls"
		}
		if st.FinishReason != "" {
			finish = st.FinishReason
		}
		emitChunk([]byte(`{}`), finish)
		// usage chunk (optional, OpenAI sometimes includes usage on final)
		if st.PromptTokens > 0 || st.CompletionTokens > 0 {
			usageChunk := []byte(`{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
			usageChunk, _ = sjson.SetBytes(usageChunk, "id", firstNonEmpty(st.ID, "chatcmpl-lite"))
			usageChunk, _ = sjson.SetBytes(usageChunk, "created", st.Created)
			usageChunk, _ = sjson.SetBytes(usageChunk, "model", firstNonEmpty(st.Model, modelName))
			usageChunk, _ = sjson.SetBytes(usageChunk, "usage.prompt_tokens", st.PromptTokens)
			usageChunk, _ = sjson.SetBytes(usageChunk, "usage.prompt_tokens_details.cached_tokens", st.CachedTokens)
			usageChunk, _ = sjson.SetBytes(usageChunk, "usage.completion_tokens", st.CompletionTokens)
			usageChunk, _ = sjson.SetBytes(usageChunk, "usage.total_tokens", st.TotalTokens)
			out = append(out, append([]byte("data: "), usageChunk...))
		}
		out = append(out, []byte("data: [DONE]"))
	case "response.failed":
		emitChunk([]byte(`{}`), "stop")
		out = append(out, []byte("data: [DONE]"))
	}
	return out
}

// ConvertOpenAIResponsesResponseToOpenAIChatCompletionsNonStream converts a full
// Responses object into a chat.completion object.
func ConvertOpenAIResponsesResponseToOpenAIChatCompletionsNonStream(_ context.Context, modelName string, _, _, rawJSON []byte, _ *any) []byte {
	root := gjson.ParseBytes(rawJSON)
	// If SSE was collected, try last JSON object; else parse as response object.
	if !root.Get("output").Exists() {
		// Attempt to find a response.completed payload in concatenated SSE.
		for _, line := range bytes.Split(rawJSON, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if bytes.HasPrefix(line, []byte("data:")) {
				line = bytes.TrimSpace(line[5:])
			}
			if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
				continue
			}
			r := gjson.ParseBytes(line)
			if r.Get("type").String() == "response.completed" {
				root = r.Get("response")
				break
			}
			if r.Get("object").String() == "response" || r.Get("output").Exists() {
				root = r
			}
		}
	}
	return responsesObjectToChatCompletion(root, modelName)
}

func responsesObjectToChatCompletion(root gjson.Result, modelName string) []byte {
	out := []byte(`{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
	id := root.Get("id").String()
	if id == "" {
		id = "chatcmpl-lite"
	}
	out, _ = sjson.SetBytes(out, "id", id)
	created := root.Get("created_at").Int()
	if created == 0 {
		created = time.Now().Unix()
	}
	out, _ = sjson.SetBytes(out, "created", created)
	model := root.Get("model").String()
	if model == "" {
		model = modelName
	}
	out, _ = sjson.SetBytes(out, "model", model)

	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls [][]byte
	root.Get("output").ForEach(func(_, item gjson.Result) bool {
		switch item.Get("type").String() {
		case "message":
			item.Get("content").ForEach(func(_, part gjson.Result) bool {
				switch part.Get("type").String() {
				case "output_text", "text":
					content.WriteString(part.Get("text").String())
				}
				return true
			})
		case "function_call":
			tc := []byte(`{"id":"","type":"function","function":{"name":"","arguments":""}}`)
			tc, _ = sjson.SetBytes(tc, "id", firstNonEmpty(item.Get("call_id").String(), item.Get("id").String()))
			tc, _ = sjson.SetBytes(tc, "function.name", item.Get("name").String())
			tc, _ = sjson.SetBytes(tc, "function.arguments", item.Get("arguments").String())
			toolCalls = append(toolCalls, tc)
		case "reasoning":
			item.Get("summary").ForEach(func(_, part gjson.Result) bool {
				reasoning.WriteString(part.Get("text").String())
				return true
			})
			if t := item.Get("content").String(); t != "" {
				reasoning.WriteString(t)
			}
		}
		return true
	})
	out, _ = sjson.SetBytes(out, "choices.0.message.content", content.String())
	if reasoning.Len() > 0 {
		out, _ = sjson.SetBytes(out, "choices.0.message.reasoning_content", reasoning.String())
	}
	if len(toolCalls) > 0 {
		for i, tc := range toolCalls {
			out, _ = sjson.SetRawBytes(out, fmt.Sprintf("choices.0.message.tool_calls.%d", i), tc)
		}
		out, _ = sjson.SetBytes(out, "choices.0.finish_reason", "tool_calls")
	}
	if usage := root.Get("usage"); usage.Exists() {
		out, _ = sjson.SetBytes(out, "usage.prompt_tokens", usage.Get("input_tokens").Int())
		out, _ = sjson.SetBytes(out, "usage.prompt_tokens_details.cached_tokens", usage.Get("input_tokens_details.cached_tokens").Int())
		out, _ = sjson.SetBytes(out, "usage.completion_tokens", usage.Get("output_tokens").Int())
		out, _ = sjson.SetBytes(out, "usage.total_tokens", usage.Get("total_tokens").Int())
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ensureToolCall registers a tool accumulator under both call_id and item_id.
// Official Responses streams use call_id on output_item.added and item_id (item.id)
// on function_call_arguments.delta — those must resolve to the same accumulator.
func (st *responsesToChatState) ensureToolCall(callID, itemID, name string, outputIndex gjson.Result) *toolCallAcc {
	if acc := st.lookupToolCall(callID, itemID, outputIndex); acc != nil {
		if name != "" && acc.Name == "" {
			acc.Name = name
		}
		if callID != "" && acc.ID == "" {
			acc.ID = callID
		}
		if itemID != "" && acc.ItemID == "" {
			acc.ItemID = itemID
		}
		st.indexToolCall(acc, outputIndex)
		return acc
	}

	primary := firstNonEmpty(callID, itemID)
	if primary == "" {
		primary = fmt.Sprintf("call_%d", len(st.ToolOrder))
	}
	acc := &toolCallAcc{
		ID:     firstNonEmpty(callID, primary),
		ItemID: itemID,
		Name:   name,
		Index:  len(st.ToolOrder),
	}
	st.ToolOrder = append(st.ToolOrder, acc.ID)
	st.indexToolCall(acc, outputIndex)
	return acc
}

func (st *responsesToChatState) lookupToolCall(callID, itemID string, outputIndex gjson.Result) *toolCallAcc {
	if st.ToolCalls == nil {
		st.ToolCalls = make(map[string]*toolCallAcc)
	}
	if callID != "" {
		if acc := st.ToolCalls[callID]; acc != nil {
			return acc
		}
	}
	if itemID != "" {
		if acc := st.ToolCalls[itemID]; acc != nil {
			return acc
		}
	}
	if outputIndex.Exists() && st.ToolByOutputIdx != nil {
		if acc := st.ToolByOutputIdx[int(outputIndex.Int())]; acc != nil {
			return acc
		}
	}
	return nil
}

func (st *responsesToChatState) indexToolCall(acc *toolCallAcc, outputIndex gjson.Result) {
	if st.ToolCalls == nil {
		st.ToolCalls = make(map[string]*toolCallAcc)
	}
	if st.ToolByOutputIdx == nil {
		st.ToolByOutputIdx = make(map[int]*toolCallAcc)
	}
	if acc.ID != "" {
		st.ToolCalls[acc.ID] = acc
	}
	if acc.ItemID != "" {
		st.ToolCalls[acc.ItemID] = acc
	}
	if outputIndex.Exists() {
		st.ToolByOutputIdx[int(outputIndex.Int())] = acc
	}
}
