package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"os/exec"
)

// ollamaRequest makes a POST request to Ollama API endpoint with payload, logs if verbose
func ollamaRequest(endpoint string, payload map[string]any) (map[string]any, error) {
	// Add keep alive to payload
	payload["keep_alive"] = appCtx.Config.OllamaKeepAlive
	jsonData, err := json.Marshal(payload)
	if err != nil {
		appCtx.ErrorLogger.Printf("error marshaling payload for Ollama %s: %v", endpoint, err)
		return nil, fmt.Errorf("error marshaling payload: %w", err)
	}

	url := appCtx.Config.OllamaBase + endpoint
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		appCtx.ErrorLogger.Printf("error creating request for Ollama %s: %v", endpoint, err)
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if appCtx.Config.VerboseDiskLogs {
		dump, _ := httputil.DumpRequestOut(req, true)
		appCtx.AccessLogger.Printf("Ollama HTTP request:\n%s", string(dump))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		appCtx.ErrorLogger.Printf("Ollama request to %s failed: %v", endpoint, err)
		return nil, fmt.Errorf("error calling Ollama %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	// Read whole response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		appCtx.ErrorLogger.Printf("error reading Ollama response from %s: %v", endpoint, err)
		return nil, fmt.Errorf("error reading response: %w", err)
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Ollama HTTP response raw:\n%s", string(bodyBytes))
	}

	if resp.StatusCode != http.StatusOK {
		appCtx.ErrorLogger.Printf("Ollama %s returned status %d: %s", endpoint, resp.StatusCode, string(bodyBytes))
		return nil, fmt.Errorf("ollama %s returned status %d", endpoint, resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		appCtx.ErrorLogger.Printf("error decoding Ollama response from %s: %v", endpoint, err)
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	if appCtx.Config.VerboseDiskLogs {
		resultJSON, _ := json.Marshal(result)
		appCtx.AccessLogger.Printf("Ollama response from %s: %s", endpoint, string(resultJSON))
	} else {
		appCtx.AccessLogger.Printf("Ollama response from %s received", endpoint)
	}

	return result, nil
}

// embedText generates a 4096-dimensional vector for the given text using Ollama embeddings API
func embedText(text string) ([]float32, error) {
	if appCtx.Config.OllamaUnloadBeforeEmbedding {
		err := exec.Command("ollama", "stop", appCtx.Config.MainModel).Run()
		if err != nil {
			appCtx.AccessLogger.Printf("Unable to unload model %s: %v", appCtx.Config.MainModel, err)
		}
	}
	result, err := ollamaRequest(appCtx.Config.EmbeddingsEndpoint, map[string]any{
		"model":  appCtx.Config.EmbeddingModel,
		"prompt": text,
	})
	if err != nil {
		return nil, fmt.Errorf("error getting embeddings: %w", err)
	}

	embedding, ok := result["embedding"].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid embedding format in response")
	}

	vector := make([]float32, len(embedding))
	for i, v := range embedding {
		if f, ok := v.(float64); ok {
			vector[i] = float32(f)
		} else {
			return nil, fmt.Errorf("embedding value not float64 at index %d", i)
		}
	}

	if len(vector) != 4096 {
		return nil, fmt.Errorf("expected 4096-dim vector, got %d", len(vector))
	}

	return vector, nil
}
