package pexels

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

type Photo struct {
	Src struct {
		Large string `json:"large"`
	} `json:"src"`
}

type PexelsResponse struct {
	Photos []Photo `json:"photos"`
}

func FetchImage(theme string) (string, error) {

	log.Printf("🔍 Запрос к Pexels: %s (оригинал: %s)", theme)

	client := &http.Client{}
	req, _ := http.NewRequest("GET", "https://api.pexels.com/v1/search", nil)
	req.Header.Add("Authorization", os.Getenv("PEXELS_API_KEY"))

	q := req.URL.Query()
	q.Add("query", theme)
	q.Add("per_page", "1")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data PexelsResponse
	json.Unmarshal(body, &data)

	if len(data.Photos) == 0 {
		return "", fmt.Errorf("no images found")
	}

	return data.Photos[0].Src.Large, nil
}
