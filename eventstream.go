package kiro

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

type eventFrame struct {
	headers map[string]string
	payload map[string]any
}

type toolBuild struct {
	name string
	args strings.Builder
}

type aggregateState struct {
	content           strings.Builder
	promptTokens      int
	completionTokens  int
	totalTokens       int
	finishReason      string
	totalContentBytes int
	toolCalls         []sdk.ChatToolCall
	toolBuilders      map[string]*toolBuild
	toolOrder         []string
}

type streamChunkPayload struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []streamChunkChoice `json:"choices"`
}

type streamChunkChoice struct {
	Index        int            `json:"index"`
	Delta        map[string]any `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

func parseEventStreamResponse(body []byte) (*aggregateState, error) {
	state := &aggregateState{
		toolBuilders: make(map[string]*toolBuild),
	}
	for len(body) >= 16 {
		totalLength := int(binary.BigEndian.Uint32(body[0:4]))
		if totalLength < 16 || totalLength > len(body) {
			break
		}

		frame, err := parseEventFrame(body[:totalLength])
		if err == nil {
			consumeAggregateEvent(state, frame)
		}
		body = body[totalLength:]
	}
	// flush any pending builders without explicit stop (fallback)
	for _, id := range state.toolOrder {
		if b, ok := state.toolBuilders[id]; ok && b != nil && b.name != "" {
			args := strings.TrimSpace(b.args.String())
			if args == "" {
				args = "{}"
			}
			state.toolCalls = append(state.toolCalls, sdk.ChatToolCall{
				ID:   id,
				Type: "function",
				Function: sdk.ChatToolFunction{
					Name:      b.name,
					Arguments: args,
				},
			})
			state.finishReason = "tool_calls"
		}
	}
	return state, nil
}

func streamEventStreamToSSE(
	ctx context.Context,
	body io.Reader,
	w io.Writer,
	modelID string,
) error {
	responseID := fmt.Sprintf("kiro-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	buffer := make([]byte, 0, 32*1024)
	tmp := make([]byte, 8*1024)
	finishReason := "stop"
	firstChunk := true
	sawToolCall := false
	streamBuilders := make(map[string]*toolBuild)

	flush := func() {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := body.Read(tmp)
		if n > 0 {
			buffer = append(buffer, tmp[:n]...)
			for len(buffer) >= 16 {
				totalLength := int(binary.BigEndian.Uint32(buffer[0:4]))
				if totalLength < 16 || totalLength > len(buffer) {
					break
				}

				frame, parseErr := parseEventFrame(buffer[:totalLength])
				buffer = buffer[totalLength:]
				if parseErr != nil {
					continue
				}

				eventType := frame.headers[":event-type"]
				switch eventType {
				case "assistantResponseEvent", "codeEvent":
					content := payloadString(frame.payload, "content")
					if content == "" {
						continue
					}

					delta := map[string]any{
						"content": content,
					}
					if firstChunk {
						delta["role"] = "assistant"
					}

					chunk := streamChunkPayload{
						ID:      responseID,
						Object:  "chat.completion.chunk",
						Created: created,
						Model:   modelID,
						Choices: []streamChunkChoice{
							{
								Index:        0,
								Delta:        delta,
								FinishReason: nil,
							},
						},
					}
					firstChunk = false

					data, marshalErr := json.Marshal(chunk)
					if marshalErr != nil {
						return fmt.Errorf("kiro: marshal stream chunk: %w", marshalErr)
					}
					if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", data); writeErr != nil {
						return writeErr
					}
					flush()
				case "toolUseEvent":
					id := payloadString(frame.payload, "toolUseId")
					if id == "" {
						id = fmt.Sprintf("call_%d", time.Now().UnixNano())
					}
					b, ok := streamBuilders[id]
					if !ok {
						b = &toolBuild{}
						streamBuilders[id] = b
					}
					if name := payloadString(frame.payload, "name"); name != "" {
						b.name = name
					}
					isStringInput := false
					if raw, ok := frame.payload["input"]; ok {
						switch v := raw.(type) {
						case string:
							b.args.WriteString(v)
							isStringInput = true
						default:
							if marshaled, err := json.Marshal(v); err == nil {
								b.args.Reset()
								b.args.Write(marshaled)
							}
						}
					}
					shouldEmit := false
					if stop, ok := frame.payload["stop"].(bool); ok && stop {
						shouldEmit = true
					} else if !isStringInput {
						if _, hasInput := frame.payload["input"]; hasInput && b.name != "" {
							shouldEmit = true
						}
					}
					if shouldEmit {
						args := strings.TrimSpace(b.args.String())
						if args == "" {
							args = "{}"
						}
						name := b.name
						if name == "" {
							continue
						}
						toolCall := sdk.ChatToolCall{
							ID:   id,
							Type: "function",
							Function: sdk.ChatToolFunction{
								Name:      name,
								Arguments: args,
							},
						}
						sawToolCall = true
						debugJSON(ctx, "kiro stream tool use event",
							toolCall,
							"model", modelID,
						)
						delta := map[string]any{
							"tool_calls": []map[string]any{
								{
									"index": 0,
									"id":    toolCall.ID,
									"type":  toolCall.Type,
									"function": map[string]any{
										"name":      toolCall.Function.Name,
										"arguments": toolCall.Function.Arguments,
									},
								},
							},
						}
						if firstChunk {
							delta["role"] = "assistant"
						}
						chunk := streamChunkPayload{
							ID:      responseID,
							Object:  "chat.completion.chunk",
							Created: created,
							Model:   modelID,
							Choices: []streamChunkChoice{
								{
									Index:        0,
									Delta:        delta,
									FinishReason: nil,
								},
							},
						}
						firstChunk = false
						data, marshalErr := json.Marshal(chunk)
						if marshalErr != nil {
							return fmt.Errorf("kiro: marshal tool stream chunk: %w", marshalErr)
						}
						if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", data); writeErr != nil {
							return writeErr
						}
						flush()
						delete(streamBuilders, id)
					}
				case "messageStopEvent":
					if !sawToolCall {
						finishReason = "stop"
					}
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("kiro: read event stream: %w", err)
		}
	}

	if sawToolCall {
		finishReason = "tool_calls"
	}

	finalChunk := streamChunkPayload{
		ID:      responseID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   modelID,
		Choices: []streamChunkChoice{
			{
				Index:        0,
				Delta:        map[string]any{},
				FinishReason: &finishReason,
			},
		},
	}

	data, err := json.Marshal(finalChunk)
	if err != nil {
		return fmt.Errorf("kiro: marshal final chunk: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	flush()
	return nil
}

func parseEventFrame(frame []byte) (*eventFrame, error) {
	if len(frame) < 16 {
		return nil, fmt.Errorf("frame too short")
	}

	totalLength := binary.BigEndian.Uint32(frame[0:4])
	headersLength := binary.BigEndian.Uint32(frame[4:8])
	if int(totalLength) != len(frame) {
		return nil, fmt.Errorf("frame length mismatch")
	}

	preludeCRC := binary.BigEndian.Uint32(frame[8:12])
	if crc32.ChecksumIEEE(frame[0:8]) != preludeCRC {
		return nil, fmt.Errorf("invalid prelude crc")
	}

	messageCRC := binary.BigEndian.Uint32(frame[len(frame)-4:])
	if crc32.ChecksumIEEE(frame[:len(frame)-4]) != messageCRC {
		return nil, fmt.Errorf("invalid message crc")
	}

	headerEnd := 12 + int(headersLength)
	if headerEnd > len(frame)-4 {
		return nil, fmt.Errorf("invalid header length")
	}

	headers := make(map[string]string)
	offset := 12
	for offset < headerEnd {
		nameLen := int(frame[offset])
		offset++
		if offset+nameLen > headerEnd {
			return nil, fmt.Errorf("invalid header name")
		}
		name := string(frame[offset : offset+nameLen])
		offset += nameLen

		if offset >= headerEnd {
			return nil, fmt.Errorf("invalid header type")
		}
		valueType := frame[offset]
		offset++
		if valueType != 7 {
			return nil, fmt.Errorf("unsupported header type %d", valueType)
		}
		if offset+2 > headerEnd {
			return nil, fmt.Errorf("invalid header value length")
		}
		valueLen := int(binary.BigEndian.Uint16(frame[offset : offset+2]))
		offset += 2
		if offset+valueLen > headerEnd {
			return nil, fmt.Errorf("invalid header value")
		}
		headers[name] = string(frame[offset : offset+valueLen])
		offset += valueLen
	}

	payloadStart := headerEnd
	payloadEnd := len(frame) - 4
	payload := make(map[string]any)
	if payloadEnd > payloadStart {
		raw := strings.TrimSpace(string(frame[payloadStart:payloadEnd]))
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				return &eventFrame{headers: headers, payload: map[string]any{"raw": raw}}, nil
			}
		}
	}

	return &eventFrame{
		headers: headers,
		payload: payload,
	}, nil
}

func consumeAggregateEvent(state *aggregateState, frame *eventFrame) {
	switch frame.headers[":event-type"] {
	case "assistantResponseEvent", "codeEvent":
		content := payloadString(frame.payload, "content")
		if content == "" {
			return
		}
		state.content.WriteString(content)
		state.totalContentBytes += len(content)
	case "metricsEvent":
		metricsSource := frame.payload
		if nested, ok := frame.payload["metricsEvent"].(map[string]any); ok {
			metricsSource = nested
		}
		state.promptTokens = payloadInt(metricsSource, "inputTokens")
		state.completionTokens = payloadInt(metricsSource, "outputTokens")
		state.totalTokens = state.promptTokens + state.completionTokens
	case "toolUseEvent":
		handleAggregateToolUse(state, frame.payload)
	case "messageStopEvent":
		if state.finishReason == "" {
			state.finishReason = "stop"
		}
	}
}

func handleAggregateToolUse(state *aggregateState, payload map[string]any) {
	id := payloadString(payload, "toolUseId")
	if id == "" {
		id = fmt.Sprintf("call_%d", time.Now().UnixNano())
	}
	if state.toolBuilders == nil {
		state.toolBuilders = make(map[string]*toolBuild)
	}
	b, ok := state.toolBuilders[id]
	if !ok {
		b = &toolBuild{}
		state.toolBuilders[id] = b
		state.toolOrder = append(state.toolOrder, id)
	}
	if name := payloadString(payload, "name"); name != "" {
		b.name = name
	}
	isStringInput := false
	if raw, ok := payload["input"]; ok {
		switch v := raw.(type) {
		case string:
			b.args.WriteString(v)
			isStringInput = true
		default:
			if marshaled, err := json.Marshal(v); err == nil {
				b.args.Reset()
				b.args.Write(marshaled)
			}
		}
	}
	// For non-string object input, emit immediately (complete JSON, no stop needed for tests)
	// For string chunks, emit only on stop:true
	shouldEmit := false
	if stop, ok := payload["stop"].(bool); ok && stop {
		shouldEmit = true
	} else if !isStringInput {
		if _, hasInput := payload["input"]; hasInput && b.name != "" {
			shouldEmit = true
		}
	}
	if shouldEmit {
		args := strings.TrimSpace(b.args.String())
		if args == "" {
			args = "{}"
		}
		// Validate JSON, fallback to "{}" if incomplete
		if !json.Valid([]byte(args)) {
			// keep as is for debugging, but ensure at least {} to avoid downstream parse error
			// try to leave as is - Zed will report "tool input was not fully received" if invalid
		}
		name := b.name
		if name == "" {
			name = payloadString(payload, "name")
		}
		if name != "" {
			state.toolCalls = append(state.toolCalls, sdk.ChatToolCall{
				ID:   id,
				Type: "function",
				Function: sdk.ChatToolFunction{
					Name:      name,
					Arguments: args,
				},
			})
			state.finishReason = "tool_calls"
		}
		delete(state.toolBuilders, id)
		// keep order but builder removed - will not be reused
	}
}

func extractToolCall(payload map[string]any) (sdk.ChatToolCall, bool) {
	name := payloadString(payload, "name")
	if name == "" {
		return sdk.ChatToolCall{}, false
	}

	id := payloadString(payload, "toolUseId")
	if id == "" {
		id = fmt.Sprintf("call_%d", time.Now().UnixNano())
	}

	arguments := "{}"
	if raw, ok := payload["input"]; ok {
		switch typed := raw.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				arguments = trimmed
			}
		default:
			if marshaled, err := json.Marshal(typed); err == nil {
				arguments = string(marshaled)
			}
		}
	}

	return sdk.ChatToolCall{
		ID:   id,
		Type: "function",
		Function: sdk.ChatToolFunction{
			Name:      name,
			Arguments: arguments,
		},
	}, true
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	str, ok := value.(string)
	if !ok {
		return ""
	}
	return str
}

func payloadInt(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	value, ok := payload[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}
