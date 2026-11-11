package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var ollamaHost = getEnvOrDefault("OLLAMA_HOST", "http://localhost:11434")

type OllamaModel struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

type ModelsResponse struct {
	Models []OllamaModel `json:"models"`
}

type ChatRequest struct {
	Model    string                 `json:"model"`
	Messages []Message              `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
	Think    *bool                  `json:"think,omitempty"`
}

type Message struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	Images    []string          `json:"images,omitempty"`
	Reasoning string            `json:"reasoning,omitempty"`
	Extra     map[string]string `json:"extra_tokens,omitempty"`
}

type ChatChunk struct {
	Message struct {
		Role             string `json:"role"`
		Content          string `json:"content"`
		ReasoningContent string `json:"reasoning_content"`
		Thinking         string `json:"thinking"`
	} `json:"message"`
	Done            bool  `json:"done"`
	PromptEvalCount int   `json:"prompt_eval_count"`
	EvalCount       int   `json:"eval_count"`
	TotalDuration   int64 `json:"total_duration"`
}

type ModelInfoResponse struct {
	Parameters json.RawMessage `json:"parameters"`
	Template   string          `json:"template,omitempty"`
	Details    struct {
		Family            string `json:"family"`
		ParameterSize     string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
	} `json:"details,omitempty"`
	ModelInfo map[string]interface{} `json:"model_info,omitempty"`
}

var showFields = []struct {
	Suffix string
	Label  string
}{
	{"context_length", "Context length"},
	{"embedding_length", "Embedding length"},
	{"block_count", "Number of layers"},
	{"feed_forward_length", "Feed-forward length"},
	{"head_count", "Attention heads"},
	{"head_count_kv", "Key/value heads (GQA)"},
	{"rope.dimension_count", "RoPE dimension"},
	{"rope.freq_base", "RoPE frequency base"},
	{"rope.dimension_count_swa", "RoPE dim (sliding window)"},
	{"rope.freq_base_swa", "RoPE base (sliding window)"},
	{"expert_count", "Total MoE experts"},
	{"expert_used_count", "Active experts per token"},
	{"parameter_count", "Total parameters"},
	{"quantization_version", "Quantization version (GGUF)"},
	{"sliding_window", "Sliding window size"},
	{"tokenizer.ggml.model", "Tokenizer type"},
}

func fetchModelInfo(model string) (*ModelInfoResponse, error) {
	body := map[string]string{"name": model}
	payload, _ := json.Marshal(body)
	resp, err := http.Post(ollamaHost+"/api/show", "application/json",
		strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var info ModelInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func fetchModels() ([]OllamaModel, error) {
	resp, err := http.Get(strings.TrimRight(ollamaHost, "/") + "/api/tags")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ollama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var result ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return result.Models, nil
}

func stopModel(model string) error {
	body := map[string]interface{}{
		"model":      model,
		"keep_alive": 0,
	}
	payload, _ := json.Marshal(body)
	resp, err := http.Post(ollamaHost+"/api/generate", "application/json",
		strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, errResp.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

type chatResult struct {
	Content       string
	Reasoning     string
	ExtraTokens   map[string]string
	PromptTokens  int
	EvalTokens    int
	TotalDuration int64
}

func streamChat(req ChatRequest) (*chatResult, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := http.Post(ollamaHost+"/api/chat", "application/json",
		strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("Ollama HTTP %d: %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("Ollama returned status %d", resp.StatusCode)
	}

	res := &chatResult{ExtraTokens: make(map[string]string)}
	var thinkingActive, prefixPrinted bool

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 512*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var chunk ChatChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}

		msg := chunk.Message
		reasonChunk := msg.ReasoningContent
		if reasonChunk == "" {
			reasonChunk = msg.Thinking
		}
		if reasonChunk != "" {
			if !thinkingActive {
				fmt.Printf("\n%s%s%sThinking...%s\n", grey, dim, italic, reset)
				thinkingActive = true
			}
			fmt.Print(grey + dim + italic + reasonChunk + reset)
			res.Reasoning += reasonChunk
			continue
		}

		content := msg.Content
		if content != "" {
			if thinkingActive {
				fmt.Printf("\n%s%s%s...done thinking.%s\n", grey, dim, italic, reset)
				fmt.Printf("%s%sAssistant:%s ", bold, magenta, reset)
				thinkingActive = false
				prefixPrinted = true
			}
			if !thinkingActive && res.Content == "" && !prefixPrinted {
				fmt.Printf("%s%sAssistant:%s ", bold, blue, reset)
				prefixPrinted = true
			}
			fmt.Print(blue + content + reset)
			res.Content += content
		}

		var rawMsg map[string]interface{}
		_ = json.Unmarshal([]byte(line), &rawMsg)
		if msgMap, ok := rawMsg["message"].(map[string]interface{}); ok {
			for key, val := range msgMap {
				switch key {
				case "role", "content", "reasoning_content", "thinking":
					continue
				}
				if val == nil {
					continue
				}
				res.ExtraTokens[key] += fmt.Sprint(val)
			}
		}

		if chunk.Done || chunk.PromptEvalCount > 0 || chunk.EvalCount > 0 {
			res.PromptTokens = chunk.PromptEvalCount
			res.EvalTokens = chunk.EvalCount
			res.TotalDuration = chunk.TotalDuration
		}
	}

	if err := scanner.Err(); err != nil {
		return res, fmt.Errorf("reading stream: %w", err)
	}
	return res, nil
}
