package directorclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// DirectorClient Director Serviceとの通信クライアント
type DirectorClient struct {
	baseURL string
	client  *http.Client
}

// NowPlaying 現在の番組情報
type NowPlaying struct {
	Theme      string    `json:"theme"`
	Segment    string    `json:"segment"`
	NextTick   time.Time `json:"nextTickAt"`
	Listeners  int       `json:"listeners"`
	Prompt     string    `json:"prompt"`
	QueueCount int       `json:"queueCount"`
	TopQueue   []string  `json:"topQueue"`
}

// Status Director Serviceの状態
type Status struct {
	IsRunning    bool      `json:"isRunning"`
	CurrentTheme string    `json:"currentTheme"`
	CurrentSeg   string    `json:"currentSegment"`
	Uptime       time.Time `json:"uptime"`
	LastUpdate   time.Time `json:"lastUpdate"`
}

// NewDirectorClient 新しいDirectorクライアントを作成
func NewDirectorClient() *DirectorClient {
	baseURL := os.Getenv("DIRECTOR_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8081" // デフォルト値
	}

	return &DirectorClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetNowPlaying 現在の番組情報を取得
func (d *DirectorClient) GetNowPlaying() (*NowPlaying, error) {
	url := fmt.Sprintf("%s/v1/now", d.baseURL)
	resp, err := d.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get now playing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("director service returned status %d", resp.StatusCode)
	}

	var nowPlaying NowPlaying
	if err := json.NewDecoder(resp.Body).Decode(&nowPlaying); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &nowPlaying, nil
}

// AdvanceSegment セグメントを進行
func (d *DirectorClient) AdvanceSegment() (*NowPlaying, error) {
	url := fmt.Sprintf("%s/v1/admin/advance", d.baseURL)
	resp, err := d.client.Post(url, "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to advance segment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("director service returned status %d", resp.StatusCode)
	}

	var nowPlaying NowPlaying
	if err := json.NewDecoder(resp.Body).Decode(&nowPlaying); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &nowPlaying, nil
}

// SetTheme テーマを設定
func (d *DirectorClient) SetTheme(theme string) (*NowPlaying, error) {
	payload := map[string]interface{}{
		"theme": theme,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal theme: %w", err)
	}

	url := fmt.Sprintf("%s/v1/admin/theme", d.baseURL)
	resp, err := d.client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to set theme: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("director service returned status %d", resp.StatusCode)
	}

	var nowPlaying NowPlaying
	if err := json.NewDecoder(resp.Body).Decode(&nowPlaying); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &nowPlaying, nil
}

// UpdatePrompt プロンプトを更新
func (d *DirectorClient) UpdatePrompt(prompt string) error {
	payload := map[string]interface{}{
		"prompt": prompt,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal prompt: %w", err)
	}

	url := fmt.Sprintf("%s/v1/admin/prompt", d.baseURL)
	resp, err := d.client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to update prompt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("director service returned status %d", resp.StatusCode)
	}

	log.Printf("Successfully updated director prompt")
	return nil
}

// GetStatus Director Serviceの状態を取得
func (d *DirectorClient) GetStatus() (*Status, error) {
	url := fmt.Sprintf("%s/v1/status", d.baseURL)
	resp, err := d.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("director service returned status %d", resp.StatusCode)
	}

	var status Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &status, nil
}

// CheckHealth Director Serviceのヘルスチェック
func (d *DirectorClient) CheckHealth() error {
	url := fmt.Sprintf("%s/health", d.baseURL)
	resp, err := d.client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to check director health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("director service is unhealthy, status %d", resp.StatusCode)
	}

	return nil
}
