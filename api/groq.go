package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Temp     float64   `json:"temperature"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func GeneratePostFromPrompt(prompt string) (string, string, error) {
	url := "https://api.groq.com/openai/v1/chat/completions"
	apiKey := os.Getenv("GROQ_API_KEY")

	if apiKey == "" {
		return "", "", fmt.Errorf("GROQ_API_KEY не найден в .env")
	}

	// Усиленный системный промт
	systemPrompt := `Ты профессиональный копирайтер. 
Ты должен строго раскрывать заданную тему без отходов. 
Не переформулируй, не упрощай и не заменяй формулировку темы. 
Пиши конкретно, с фактами и деталями. Без воды и ИИ-стиля.

В конце обязательно добавляй JSON-блок с ключевыми словами из первого предложения (на английском), без пояснений. 
Пример: { "keywords": "technology, gadgets, innovation" }`

	reqBody := ChatRequest{
		Model: "llama3-70b-8192",
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		Temp: 0.7,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("ошибка маршалинга запроса: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", "", fmt.Errorf("ошибка создания запроса: %w", err)
	}
	req.Header.Add("Authorization", "Bearer "+apiKey)
	req.Header.Add("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("ошибка запроса: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("ошибка чтения ответа: %w", err)
	}

	var result ChatResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", "", fmt.Errorf("ошибка парсинга ответа: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", "", fmt.Errorf("пустой ответ от модели")
	}

	fullText := result.Choices[0].Message.Content
	text, keywordsJSON := splitTextAndKeywords(fullText)

	var keywordData struct {
		Keywords string `json:"keywords"`
	}
	err = json.Unmarshal([]byte(keywordsJSON), &keywordData)
	if err != nil {
		fmt.Println("⚠️ Ошибка парсинга ключевых слов:", err)
		return text, "", nil // безопасный fallback
	}

	return text, keywordData.Keywords, nil
}

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

	postText = strings.Join(lines, "\n")
	keywords = jsonLine
	return
}
func Translate(text string, toLang string) (string, error) {
	url := "https://api.groq.com/openai/v1/chat/completions"
	apiKey := os.Getenv("GROQ_API_KEY")

	if apiKey == "" {
		return "", fmt.Errorf("GROQ_API_KEY не найден в .env")
	}

	prompt := fmt.Sprintf("Переведи на английский без кавычек и пояснений: %s", text)

	reqBody := ChatRequest{
		Model: "llama3-70b-8192",
		Messages: []Message{
			{Role: "system", Content: "Ты переводчик."},
			{Role: "user", Content: prompt},
		},
		Temp: 0.3,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Add("Authorization", "Bearer "+apiKey)
	req.Header.Add("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result ChatResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("пустой ответ от модели")
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}
