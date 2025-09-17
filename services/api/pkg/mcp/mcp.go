package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// WeatherInfo 天気情報
type WeatherInfo struct {
	Location  string `json:"location"`
	Temp      string `json:"temp"`
	Condition string `json:"condition"`
	Humidity  string `json:"humidity"`
}

// NewsInfo ニュース情報
type NewsInfo struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Source  string `json:"source"`
	Time    string `json:"time"`
}

// FAQInfo FAQ情報
type FAQInfo struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Category string `json:"category"`
}

// MCPClient MCP（Model Context Protocol）クライアント
type MCPClient struct {
	openaiAPIKey string
}

// NewMCPClient 新しいMCPクライアントを作成
func NewMCPClient() *MCPClient {
	return &MCPClient{
		openaiAPIKey: os.Getenv("OPENAI_API_KEY"),
	}
}

// GetWeather 天気情報を取得
func (m *MCPClient) GetWeather(location string) (*WeatherInfo, error) {
	// 実際の実装では、OpenWeatherMap APIや他の天気APIを使用
	// ここではデモ用のダミーデータを返す
	return &WeatherInfo{
		Location:  location,
		Temp:      "22°C",
		Condition: "晴れ",
		Humidity:  "65%",
	}, nil
}

// GetNews ニュース情報を取得
func (m *MCPClient) GetNews(category string) ([]NewsInfo, error) {
	// 実際の実装では、ニュースAPIを使用
	// ここではデモ用のダミーデータを返す
	news := []NewsInfo{
		{
			Title:   "AI技術の最新動向",
			Summary: "人工知能技術が急速に発展し、様々な分野で応用が進んでいます。",
			Source:  "Tech News",
			Time:    time.Now().Format("15:04"),
		},
		{
			Title:   "気候変動への取り組み",
			Summary: "世界各国が気候変動対策に本格的に取り組んでいます。",
			Source:  "Environment News",
			Time:    time.Now().Add(-30 * time.Minute).Format("15:04"),
		},
	}
	return news, nil
}

// GetFAQ よくある質問を取得
func (m *MCPClient) GetFAQ(query string) ([]FAQInfo, error) {
	// 実際の実装では、データベースから検索
	// ここではデモ用のダミーデータを返す
	faqs := []FAQInfo{
		{
			Question: "ラジオの聴き方は？",
			Answer:   "ブラウザでアクセスするか、スマートフォンアプリをご利用ください。",
			Category: "基本操作",
		},
		{
			Question: "投稿はどのようにできますか？",
			Answer:   "PTTボタンを押して音声で投稿するか、テキストで投稿できます。",
			Category: "投稿方法",
		},
	}
	return faqs, nil
}

// GetCompanyInfo 会社情報を取得
func (m *MCPClient) GetCompanyInfo() (map[string]interface{}, error) {
	// 実際の実装では、社内データベースから取得
	info := map[string]interface{}{
		"company_name": "Radio24 Inc.",
		"established":  "2024",
		"mission":      "24時間いつでも聴けるラジオを提供",
		"contact":      "info@radio24.com",
	}
	return info, nil
}

// CallOpenAI 外部情報取得のためのOpenAI API呼び出し
func (m *MCPClient) CallOpenAI(prompt string) (string, error) {
	if m.openaiAPIKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	payload := map[string]interface{}{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 500,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions",
		io.NopCloser(bytes.NewReader(jsonData)))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+m.openaiAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error: %s", string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}

	return result.Choices[0].Message.Content, nil
}

// GenerateContextualInfo 現在の時間とテーマに基づいて文脈情報を生成
func (m *MCPClient) GenerateContextualInfo(theme string, hour int) (string, error) {
	prompt := fmt.Sprintf(`
現在のテーマ: %s
現在の時間: %d時

この情報に基づいて、ラジオのパーソナリティが話すのに適した話題や情報を提供してください。
天気、ニュース、リスナーへの呼びかけなどを含めて、自然な会話の流れを作るための情報を生成してください。
`, theme, hour)

	return m.CallOpenAI(prompt)
}
