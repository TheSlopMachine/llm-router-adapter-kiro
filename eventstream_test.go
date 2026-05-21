package kiro

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"strings"
	"testing"
)

func TestStreamEventStreamToSSEOmitEmptyRole(t *testing.T) {
	body := bytes.NewBuffer(nil)
	body.Write(mustEventFrame(t, "assistantResponseEvent", map[string]any{"content": "Hello"}))
	body.Write(mustEventFrame(t, "assistantResponseEvent", map[string]any{"content": " there"}))
	body.Write(mustEventFrame(t, "messageStopEvent", map[string]any{}))

	var out bytes.Buffer
	if err := streamEventStreamToSSE(context.Background(), body, &out, "kiro/claude-haiku-4.5"); err != nil {
		t.Fatalf("streamEventStreamToSSE failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("unexpected SSE output: %q", out.String())
	}

	first := parseChunkLine(t, lines[0])
	second := parseChunkLine(t, lines[2])
	final := parseChunkLine(t, lines[4])

	firstDelta := first.Choices[0].Delta
	if firstDelta["role"] != "assistant" {
		t.Fatalf("expected first delta role=assistant, got %#v", firstDelta)
	}
	if firstDelta["content"] != "Hello" {
		t.Fatalf("expected first delta content, got %#v", firstDelta)
	}

	secondDelta := second.Choices[0].Delta
	if _, ok := secondDelta["role"]; ok {
		t.Fatalf("expected subsequent delta to omit role, got %#v", secondDelta)
	}
	if secondDelta["content"] != " there" {
		t.Fatalf("expected subsequent delta content, got %#v", secondDelta)
	}

	finalDelta := final.Choices[0].Delta
	if len(finalDelta) != 0 {
		t.Fatalf("expected final delta to be empty, got %#v", finalDelta)
	}
	if final.Choices[0].FinishReason == nil || *final.Choices[0].FinishReason != "stop" {
		t.Fatalf("expected final finish_reason=stop, got %#v", final.Choices[0].FinishReason)
	}
}

func TestStreamEventStreamToSSEEmitsToolCalls(t *testing.T) {
	body := bytes.NewBuffer(nil)
	body.Write(mustEventFrame(t, "toolUseEvent", map[string]any{
		"toolUseId": "call_1",
		"name":      "TodoWrite",
		"input": map[string]any{
			"items": []string{"a"},
		},
	}))
	body.Write(mustEventFrame(t, "messageStopEvent", map[string]any{}))

	var out bytes.Buffer
	if err := streamEventStreamToSSE(context.Background(), body, &out, "kiro/claude-haiku-4.5"); err != nil {
		t.Fatalf("streamEventStreamToSSE failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("unexpected SSE output: %q", out.String())
	}

	first := parseChunkLine(t, lines[0])
	final := parseChunkLine(t, lines[2])

	deltaToolCalls, ok := first.Choices[0].Delta["tool_calls"].([]any)
	if !ok || len(deltaToolCalls) != 1 {
		t.Fatalf("expected one tool call delta, got %#v", first.Choices[0].Delta)
	}
	if first.Choices[0].Delta["role"] != "assistant" {
		t.Fatalf("expected first tool delta to include assistant role, got %#v", first.Choices[0].Delta)
	}
	if final.Choices[0].FinishReason == nil || *final.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("expected final finish_reason=tool_calls, got %#v", final.Choices[0].FinishReason)
	}
}

func parseChunkLine(t *testing.T, line string) streamChunkPayload {
	t.Helper()
	if !strings.HasPrefix(line, "data: ") {
		t.Fatalf("unexpected SSE line: %q", line)
	}
	var chunk streamChunkPayload
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
		t.Fatalf("unmarshal chunk: %v", err)
	}
	return chunk
}

func mustEventFrame(t *testing.T, eventType string, payload map[string]any) []byte {
	t.Helper()

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	headers := encodeStringHeader(":event-type", eventType)
	totalLength := 12 + len(headers) + len(payloadBytes) + 4

	frame := bytes.NewBuffer(make([]byte, 0, totalLength))
	writeU32(frame, uint32(totalLength))
	writeU32(frame, uint32(len(headers)))

	prelude := frame.Bytes()
	writeU32(frame, crc32.ChecksumIEEE(prelude))

	frame.Write(headers)
	frame.Write(payloadBytes)

	messageCRC := crc32.ChecksumIEEE(frame.Bytes())
	writeU32(frame, messageCRC)
	return frame.Bytes()
}

func encodeStringHeader(name, value string) []byte {
	header := bytes.NewBuffer(nil)
	header.WriteByte(byte(len(name)))
	header.WriteString(name)
	header.WriteByte(7)
	writeU16(header, uint16(len(value)))
	header.WriteString(value)
	return header.Bytes()
}

func writeU16(buf *bytes.Buffer, value uint16) {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], value)
	buf.Write(tmp[:])
}

func writeU32(buf *bytes.Buffer, value uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], value)
	buf.Write(tmp[:])
}
