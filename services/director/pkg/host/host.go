package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// HostClient Hostエージェントとの通信クライアント
type HostClient struct {
	baseURL string
	client  *http.Client
}

// NewHostClient 新しいHostクライアントを作成
func NewHostClient() *HostClient {
	baseURL := os.Getenv("HOST_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080" // デフォルト値
	}

	return &HostClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SendInstruction Hostエージェントに指示を送信
func (h *HostClient) SendInstruction(instruction string) error {
	payload := map[string]interface{}{
		"type":    "director_instruction",
		"content": instruction,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal instruction: %w", err)
	}

	url := fmt.Sprintf("%s/director/instruction", h.baseURL)
	log.Printf("Sending instruction to host at URL: %s", url)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("Failed to send HTTP request to host: %v", err)
		return fmt.Errorf("failed to send instruction: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("Host service responded with status: %d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("host service returned status %d", resp.StatusCode)
	}

	log.Printf("Successfully sent instruction to host: %s", instruction)
	return nil
}

// UpdatePrompt Hostエージェントのプロンプトを更新
func (h *HostClient) UpdatePrompt(prompt string) error {
	payload := map[string]interface{}{
		"prompt": prompt,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal prompt: %w", err)
	}

	url := fmt.Sprintf("%s/director/prompt", h.baseURL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update prompt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("host service returned status %d", resp.StatusCode)
	}

	log.Printf("Successfully updated host prompt")
	return nil
}

// CheckHealth Hostエージェントのヘルスチェック
func (h *HostClient) CheckHealth() error {
	url := fmt.Sprintf("%s/health", h.baseURL)
	resp, err := h.client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to check host health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("host service is unhealthy, status %d", resp.StatusCode)
	}

	return nil
}
