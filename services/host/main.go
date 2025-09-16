package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

type HostAgent struct {
	openaiConn     *websocket.Conn
	reconnectTimer *time.Timer
	ctx            context.Context
	cancel         context.CancelFunc
}

type DirectorMessage struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Theme   string `json:"theme"`
	Segment string `json:"segment"`
}

func main() {
	// 環境変数読み込み
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent := &HostAgent{
		ctx:    ctx,
		cancel: cancel,
	}

	// LiveKit Room接続（一時的に無効化）
	// if err := agent.connectToLiveKit(); err != nil {
	//	log.Fatal("Failed to connect to LiveKit:", err)
	// }

	// OpenAI Realtime接続
	if err := agent.connectToOpenAI(); err != nil {
		log.Fatal("Failed to connect to OpenAI:", err)
	}

	// メインループ
	agent.run()
}

// func (h *HostAgent) connectToLiveKit() error {
//	apiKey := getEnv("LIVEKIT_API_KEY", "devkey")
//	apiSecret := getEnv("LIVEKIT_API_SECRET", "secret")
//	wsURL := getEnv("LIVEKIT_WS_URL", "ws://localhost:7880")
//
//	// Host用のトークン生成
//	at := auth.NewAccessToken(apiKey, apiSecret)
//	grant := &auth.VideoGrant{
//		RoomJoin:     true,
//		Room:         "radio-24",
//		CanPublish:   true,
//		CanSubscribe: true,
//	}
//	at.AddGrant(grant).
//		SetIdentity("host-agent").
//		SetValidFor(time.Hour * 24)
//
//	token, err := at.ToJWT()
//	if err != nil {
//		return fmt.Errorf("failed to generate token: %w", err)
//	}
//
//	// Room接続
//	h.room, err = room.Connect(wsURL, token, &room.ConnectOptions{
//		AutoSubscribe: true,
//	})
//	if err != nil {
//		return fmt.Errorf("failed to connect to room: %w", err)
//	}
//
//	log.Println("Connected to LiveKit room")
//	return nil
// }

func (h *HostAgent) connectToOpenAI() error {
	apiKey := getEnv("OPENAI_API_KEY", "")
	if apiKey == "" {
		return fmt.Errorf("OPENAI_API_KEY not set")
	}

	// OpenAI Realtime WebSocket接続
	url := "wss://api.openai.com/v1/realtime?model=gpt-realtime"
	headers := map[string][]string{
		"Authorization": {fmt.Sprintf("Bearer %s", apiKey)},
	}

	conn, _, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		return fmt.Errorf("failed to connect to OpenAI: %w", err)
	}

	h.openaiConn = conn

	// 初期セッション設定
	sessionUpdate := map[string]interface{}{
		"type": "session.update",
		"session": map[string]interface{}{
			"type":         "realtime",
			"instructions": "あなたは24時間AIラジオのDJ。無音禁止、短文でテンポよく。Q&Aでは回答→10文字要約→次へ。",
			"voice":        "marin",
			"audio": map[string]interface{}{
				"input": map[string]interface{}{
					"turn_detection": map[string]interface{}{
						"type":            "server_vad",
						"idle_timeout_ms": 6000,
					},
				},
			},
		},
	}

	if err := conn.WriteJSON(sessionUpdate); err != nil {
		return fmt.Errorf("failed to send session update: %w", err)
	}

	log.Println("Connected to OpenAI Realtime")
	return nil
}

func (h *HostAgent) run() {
	// 音声データ受信ループ
	go h.handleOpenAIMessages()

	// 定期発話ループ（30秒ごと）
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// 初回発話
	h.sendMessage("こんにちは！Radio-24です。24時間お疲れ様です。")

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			// 定期発話（軽い話題やステーションID）
			h.sendMessage("Radio-24、24時間放送中です。")
		}
	}
}

func (h *HostAgent) handleOpenAIMessages() {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
			var msg map[string]interface{}
			if err := h.openaiConn.ReadJSON(&msg); err != nil {
				log.Printf("OpenAI connection error: %v", err)
				h.reconnectOpenAI()
				return
			}

			// 音声出力をLiveKitにPublish
			if msgType, ok := msg["type"].(string); ok {
				switch msgType {
				case "response.audio.delta":
					if audioData, ok := msg["delta"].(string); ok {
						h.publishAudioToLiveKit(audioData)
					}
				case "response.done":
					log.Println("OpenAI response completed")
				}
			}
		}
	}
}

func (h *HostAgent) sendMessage(content string) {
	message := map[string]interface{}{
		"type": "conversation.item.create",
		"item": map[string]interface{}{
			"type":    "message",
			"role":    "user",
			"content": content,
			"name":    "system",
		},
	}

	if err := h.openaiConn.WriteJSON(message); err != nil {
		log.Printf("Failed to send message: %v", err)
	}
}

func (h *HostAgent) publishAudioToLiveKit(audioData string) {
	// 音声データをLiveKitにPublish（一時的に無効化）
	// 実際の実装では、base64デコードしてオーディオトラックとして送信
	// ここでは簡略化
	log.Printf("Audio data received: %d bytes", len(audioData))
}

func (h *HostAgent) reconnectOpenAI() {
	if h.reconnectTimer != nil {
		h.reconnectTimer.Stop()
	}

	h.reconnectTimer = time.AfterFunc(5*time.Second, func() {
		log.Println("Attempting to reconnect to OpenAI...")
		if err := h.connectToOpenAI(); err != nil {
			log.Printf("Reconnection failed: %v", err)
			h.reconnectOpenAI() // 再試行
		} else {
			log.Println("Reconnected to OpenAI")
		}
	})
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
