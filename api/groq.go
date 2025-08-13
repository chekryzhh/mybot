package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

/*
ENV ПЕРЕМЕННЫЕ (пример в /opt/poster_bot/.env):

# цепочка провайдеров: по порядку пробуем groq → openrouter → (можно убрать любого)
LLM_PROVIDER_CHAIN=groq,openrouter

# --- GROQ ---
GROQ_API_KEY=...                     # обязателен для groq
GROQ_BASE_URL=https://api.groq.com/openai/v1
GROQ_MODEL=llama3-70b-8192           # или, если доступно: llama-3.3-70b-versatile

# --- OpenRouter (фоллбэк) ---
OPENROUTER_API_KEY=sk-or-...         # если задан — сможем падать на openrouter
OPENROUTER_BASE_URL=https://openrouter.ai/api/v1
OPENROUTER_MODEL=meta-llama/llama-3.1-70b-instruct

# Поведение HTTP‑клиента
LLM_TIMEOUT_SECONDS=30
*/

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

var httpClient = &http.Client{
	Timeout: time.Second * time.Duration(getIntEnv("LLM_TIMEOUT_SECONDS", 30)),
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func getIntEnv(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		var x int
		fmt.Sscanf(v, "%d", &x)
		if x > 0 {
			return x
		}
	}
	return def
}

// ========================= PUBLIC API =========================

func GeneratePostFromPrompt(prompt string) (string, string, error) {
	// Системный промпт — оставил твой, слегка подчистил
	systemPrompt := `Ты профессиональный копирайтер.
Пиши строго по заданной теме, без воды и "ИИ‑стиля". Конкретика, факты, детали.
В конце добавь отдельной строкой JSON с ключевыми словами (англ.), без пояснений.
Пример: { "keywords": "technology, gadgets, innovation" }`

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}

	// По цепочке провайдеров: groq → openrouter (можно менять через LLM_PROVIDER_CHAIN)
	providers := getProviderChain()

	var lastErr error
	for _, p := range providers {
		text, keywords, err := callProvider(p, messages, 0.7, 512)
		if err == nil {
			return text, keywords, nil
		}
		lastErr = err
		fmt.Printf("llm[%s] error: %v\n", p, err) // виден в journalctl
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no providers configured")
	}
	return "", "", lastErr
}

func Translate(text string, toLang string) (string, error) {
	if strings.TrimSpace(toLang) == "" {
		toLang = "английский"
	}

	sys := "Ты профессиональный переводчик. Переводи точно по смыслу, без кавычек и пояснений."
	user := fmt.Sprintf("Переведи на %s: %s", toLang, text)

	messages := []Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}

	providers := getProviderChain()
	var lastErr error
	for _, p := range providers {
		content, _, err := callProvider(p, messages, 0.3, 256)
		if err == nil {
			return strings.TrimSpace(content), nil
		}
		lastErr = err
		fmt.Printf("llm[%s] translate error: %v\n", p, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no providers configured")
	}
	return "", lastErr
}

// ========================= CORE =========================

func getProviderChain() []string {
	raw := getEnv("LLM_PROVIDER_CHAIN", "groq,openrouter")
	parts := strings.Split(raw, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		switch p {
		case "groq", "openrouter":
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{"groq"}
	}
	return out
}

func callProvider(provider string, messages []Message, temp float64, maxTokens int) (string, string, error) {
	switch provider {
	case "groq":
		base := getEnv("GROQ_BASE_URL", "https://api.groq.com/openai/v1")
		model := getEnv("GROQ_MODEL", "llama3-70b-8192")
		key := os.Getenv("GROQ_API_KEY")
		if key == "" {
			return "", "", fmt.Errorf("GROQ_API_KEY is empty")
		}
		return callOpenAICompat(base, key, model, messages, temp, maxTokens, nil)

	case "openrouter":
		base := getEnv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1")
		model := getEnv("OPENROUTER_MODEL", "meta-llama/llama-3.1-70b-instruct")
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			return "", "", fmt.Errorf("OPENROUTER_API_KEY is empty")
		}
		headers := map[string]string{
			"HTTP-Referer": "https://t.me/poster_refact_bot",
			"X-Title":      "Poster Bot",
		}
		return callOpenAICompat(base, key, model, messages, temp, maxTokens, headers)
	}
	return "", "", fmt.Errorf("unknown provider: %s", provider)
}

// Универсальный OpenAI‑совместимый вызов /chat/completions
func callOpenAICompat(baseURL, apiKey, model string, messages []Message, temperature float64, maxTokens int, extraHeaders map[string]string) (string, string, error) {
	if baseURL == "" || apiKey == "" {
		return "", "", fmt.Errorf("missing baseURL or apiKey")
	}
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"

	reqBody := ChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
	}
	bs, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", url, bytes.NewReader(bs))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		// Вернём статус и кусок тела — чтобы в логах было видно истинную причину (401/404/429/400 …)
		short := string(bodyBytes)
		if len(short) > 400 {
			short = short[:400] + "...(truncated)"
		}
		return "", "", fmt.Errorf("bad status %d: %s", resp.StatusCode, short)
	}

	var parsed ChatResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return "", "", fmt.Errorf("json: %v; raw=%s", err, truncate(bodyBytes, 400))
	}
	if len(parsed.Choices) == 0 {
		return "", "", fmt.Errorf("empty choices: %s", truncate(bodyBytes, 400))
	}

	fullText := strings.TrimSpace(parsed.Choices[0].Message.Content)
	text, keywordsJSON := splitTextAndKeywords(fullText)

	// Разобрать JSON с ключевыми словами, но это необязательно — мягкий фоллбэк
	kws := ""
	if keywordsJSON != "" {
		var keywordData struct {
			Keywords string `json:"keywords"`
		}
		if err := json.Unmarshal([]byte(keywordsJSON), &keywordData); err == nil {
			kws = strings.TrimSpace(keywordData.Keywords)
		} else {
			fmt.Printf("⚠️ keywords json parse error: %v\n", err)
		}
	}
	return text, kws, nil
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "...(truncated)"
	}
	return s
}

// Выдёргиваем из ответа последний JSON‑блок как keywords, остальное — текст поста.
func splitTextAndKeywords(response string) (postText string, keywords string) {
	lines := strings.Split(response, "\n")
	var jsonLine string

	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			jsonLine = line
			lines = lines[:i]
			break
		}
	}
	postText = strings.TrimSpace(strings.Join(lines, "\n"))
	keywords = strings.TrimSpace(jsonLine)
	return
}
