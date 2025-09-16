package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/livekit/protocol/auth"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

type HostAgent struct {
	openaiConn     *websocket.Conn
	livekitConn    *websocket.Conn
	peerConnection *webrtc.PeerConnection
	audioTrack     *webrtc.TrackLocalStaticSample
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
	// 環境変数読み込み（リポジトリルートの.envファイル）
	err := godotenv.Load("../../.env")
	if err != nil {
		// Docker環境では/app/.envを試す
		err = godotenv.Load("/app/.env")
		if err != nil {
			log.Println("No .env file found - using environment variables")
		} else {
			log.Println("Loaded .env file from /app/.env")
		}
	} else {
		log.Println("Loaded .env file from repository root")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent := &HostAgent{
		ctx:    ctx,
		cancel: cancel,
	}

	// LiveKit Room接続
	log.Println("Attempting to connect to LiveKit...")
	if err := agent.connectToLiveKit(); err != nil {
		log.Printf("Failed to connect to LiveKit: %v", err)
		// LiveKit接続に失敗しても続行
	} else {
		log.Println("Successfully connected to LiveKit")
	}

	// OpenAI Realtime接続
	if err := agent.connectToOpenAI(); err != nil {
		log.Fatal("Failed to connect to OpenAI:", err)
	}

	// メインループ
	agent.run()
}

func (h *HostAgent) connectToLiveKit() error {
	wsURL := getEnv("LIVEKIT_WS_URL", "ws://localhost:7880")
	log.Printf("Connecting to LiveKit at: %s", wsURL)
	log.Printf("Environment variables - LIVEKIT_API_KEY: %s, LIVEKIT_API_SECRET: %s",
		getEnv("LIVEKIT_API_KEY", "not set"),
		getEnv("LIVEKIT_API_SECRET", "not set"))

	// WebRTC PeerConnection設定
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	log.Println("Creating WebRTC PeerConnection...")
	var err error
	h.peerConnection, err = webrtc.NewPeerConnection(config)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %w", err)
	}
	log.Println("WebRTC PeerConnection created successfully")

	// オーディオトラック作成
	log.Println("Creating audio track...")
	h.audioTrack, err = webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"host-audio",
		"radio-24-host",
	)
	if err != nil {
		return fmt.Errorf("failed to create audio track: %w", err)
	}
	log.Println("Audio track created successfully")

	// トラックをPeerConnectionに追加
	log.Println("Adding track to PeerConnection...")
	_, err = h.peerConnection.AddTrack(h.audioTrack)
	if err != nil {
		return fmt.Errorf("failed to add track: %w", err)
	}
	log.Println("Track added to PeerConnection successfully")

	// LiveKit認証トークン生成
	apiKey := getEnv("LIVEKIT_API_KEY", "devkey")
	apiSecret := getEnv("LIVEKIT_API_SECRET", "secret")

	at := auth.NewAccessToken(apiKey, apiSecret)
	at.SetIdentity("radio-24-host")
	grant := &auth.VideoGrant{
		Room:         "radio-24",
		RoomJoin:     true,
		CanPublish:   &[]bool{true}[0],
		CanSubscribe: &[]bool{true}[0],
	}
	at.AddGrant(grant)
	token, err := at.ToJWT()
	if err != nil {
		return fmt.Errorf("failed to create LiveKit token: %w", err)
	}

	log.Printf("Generated LiveKit token: %s", token)

	// LiveKit WebSocket接続（認証ヘッダー付き）
	headers := map[string][]string{
		"Authorization": {fmt.Sprintf("Bearer %s", token)},
	}
	log.Printf("Connecting to LiveKit WebSocket: %s/rtc", wsURL)
	h.livekitConn, _, err = websocket.DefaultDialer.Dial(wsURL+"/rtc", headers)
	if err != nil {
		log.Printf("Failed to connect to LiveKit: %v", err)
		// LiveKit接続に失敗しても続行
		return fmt.Errorf("failed to connect to LiveKit WebSocket: %w", err)
	}

	log.Println("Connected to LiveKit room successfully")
	return nil
}

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

	// LiveKitメッセージハンドリング
	go h.handleLiveKitMessages()

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
	if h.audioTrack == nil {
		log.Println("Audio track not initialized, skipping audio publish")
		return
	}

	// Base64デコード
	audioBytes, err := base64.StdEncoding.DecodeString(audioData)
	if err != nil {
		log.Printf("Failed to decode audio data: %v", err)
		return
	}

	// オーディオトラックに送信
	if err := h.audioTrack.WriteSample(media.Sample{
		Data:     audioBytes,
		Duration: time.Millisecond * 20, // 20msのサンプル
	}); err != nil {
		log.Printf("Failed to write audio sample: %v", err)
		return
	}

	log.Printf("Audio data published: %d bytes", len(audioBytes))
}

func (h *HostAgent) handleLiveKitMessages() {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
			if h.livekitConn == nil {
				time.Sleep(time.Second)
				continue
			}

			var msg map[string]interface{}
			if err := h.livekitConn.ReadJSON(&msg); err != nil {
				log.Printf("LiveKit connection error: %v", err)
				h.reconnectLiveKit()
				return
			}

			// LiveKitメッセージの処理
			if msgType, ok := msg["type"].(string); ok {
				switch msgType {
				case "offer":
					// WebRTC Offer処理
					h.handleWebRTCOffer(msg)
				case "answer":
					// WebRTC Answer処理
					h.handleWebRTCAnswer(msg)
				case "ice-candidate":
					// ICE Candidate処理
					h.handleICECandidate(msg)
				}
			}
		}
	}
}

func (h *HostAgent) handleWebRTCOffer(msg map[string]interface{}) {
	// WebRTC Offerの処理（簡略化）
	log.Println("Received WebRTC offer from LiveKit")
}

func (h *HostAgent) handleWebRTCAnswer(msg map[string]interface{}) {
	// WebRTC Answerの処理（簡略化）
	log.Println("Received WebRTC answer from LiveKit")
}

func (h *HostAgent) handleICECandidate(msg map[string]interface{}) {
	// ICE Candidateの処理（簡略化）
	log.Println("Received ICE candidate from LiveKit")
}

func (h *HostAgent) reconnectLiveKit() {
	if h.reconnectTimer != nil {
		h.reconnectTimer.Stop()
	}

	h.reconnectTimer = time.AfterFunc(5*time.Second, func() {
		log.Println("Attempting to reconnect to LiveKit...")
		if err := h.connectToLiveKit(); err != nil {
			log.Printf("LiveKit reconnection failed: %v", err)
			h.reconnectLiveKit() // 再試行
		} else {
			log.Println("Reconnected to LiveKit")
		}
	})
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
