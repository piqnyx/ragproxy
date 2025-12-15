package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// ResponseCapture captures the response for logging
type ResponseCapture struct {
	http.ResponseWriter
	status int
	body   *bytes.Buffer
}

// StreamCollectorWriter — wrapper to collect streamed response
type StreamCollectorWriter struct {
	http.ResponseWriter
	mu        sync.Mutex
	collected bytes.Buffer
	closed    bool
	complete  bool
}

// WriteHeader captures the status code
func (rc *ResponseCapture) WriteHeader(code int) {
	rc.status = code
	rc.ResponseWriter.WriteHeader(code)
}

// Write captures the response body
func (rc *ResponseCapture) Write(data []byte) (int, error) {
	rc.body.Write(data)
	return rc.ResponseWriter.Write(data)
}

func (w *StreamCollectorWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Пробуем собрать только чанки с контентом
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"delta"`) {
			w.collected.WriteString(line)
			w.collected.WriteByte('\n')
		}
		// Mark that the stream has ended normally
		if line == "data: [DONE]" {
			w.complete = true
		}
	}
	return w.ResponseWriter.Write(data)
}

func (w *StreamCollectorWriter) CloseAndProcess(cleanUserContent string, attachments []Attachment, promptVector []float32, queryHash string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true

	// Only if the final chunk was received
	if !w.complete {
		return
	}

	var result strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(w.collected.Bytes()))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"delta"`) {
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk)
			if len(chunk.Choices) > 0 {
				result.WriteString(chunk.Choices[0].Delta.Content)
			}
		}
	}
	if result.Len() > 0 {
		processOutbound(result.String(), cleanUserContent, attachments, promptVector, queryHash)
	}
}
