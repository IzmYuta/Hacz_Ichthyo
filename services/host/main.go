package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/livekit/protocol/auth"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

type HostAgent struct {
	openaiConn     *websocket.Conn
	room           *lksdk.Room
	audioTrack     *lksdk.LocalTrack
	reconnectTimer *time.Timer
	ctx            context.Context
	cancel         context.CancelFunc
	audioBuffer    []byte
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

	// LiveKit SDKを使ってルームに接続
	log.Println("Connecting to LiveKit room using SDK...")
	h.room, err = lksdk.ConnectToRoomWithToken(wsURL, token, &lksdk.RoomCallback{})
	if err != nil {
		return fmt.Errorf("failed to connect to LiveKit room: %w", err)
	}

	// オーディオトラックを作成（Opus）
	log.Println("Creating audio track...")
	h.audioTrack, err = lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus,
	})
	if err != nil {
		return fmt.Errorf("failed to create audio track: %w", err)
	}

	// トラックをルームに公開
	log.Println("Publishing audio track to room...")
	_, err = h.room.LocalParticipant.PublishTrack(h.audioTrack, &lksdk.TrackPublicationOptions{
		Name: "radio-24-host",
	})
	if err != nil {
		return fmt.Errorf("failed to publish track: %w", err)
	}

	log.Println("Successfully connected to LiveKit room and published audio track")
	return nil
}

func (h *HostAgent) connectToOpenAI() error {
	apiKey := getEnv("OPENAI_API_KEY", "")
	log.Printf("OpenAI API key: %s", apiKey[:10]+"...") // 最初の10文字だけ表示

	if apiKey == "" || apiKey == "your-openai-api-key" || apiKey == "test-mode" {
		log.Println("OpenAI API key not set, using test mode")
		h.openaiConn = nil // テストモード
		return nil
	}

	log.Println("Attempting to connect to OpenAI Realtime API...")

	// OpenAI Realtime WebSocket接続
	url := "wss://api.openai.com/v1/realtime?model=gpt-realtime"
	headers := map[string][]string{
		"Authorization": {fmt.Sprintf("Bearer %s", apiKey)},
	}

	log.Printf("Connecting to: %s", url)
	conn, _, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		log.Printf("Failed to connect to OpenAI: %v, using test mode", err)
		h.openaiConn = nil // テストモードにフォールバック
		return nil
	}

	h.openaiConn = conn

	// 初期セッション設定
	sessionUpdate := map[string]interface{}{
		"type": "session.update",
		"session": map[string]interface{}{
			"type":              "realtime",
			"instructions":      "あなたは24時間AIラジオのDJ。無音禁止、短文でテンポよく。Q&Aでは回答→10文字要約→次へ。",
			"output_modalities": []string{"audio"},
			"audio": map[string]interface{}{
				"input": map[string]interface{}{
					"turn_detection": map[string]interface{}{
						"type":            "server_vad",
						"idle_timeout_ms": 6000,
					},
				},
				"output": map[string]interface{}{
					"voice": "marin",
				},
			},
		},
	}

	if err := conn.WriteJSON(sessionUpdate); err != nil {
		log.Printf("Failed to send session update: %v, using test mode", err)
		h.openaiConn = nil // テストモードにフォールバック
		return nil
	}

	log.Println("Connected to OpenAI Realtime")
	return nil
}

func (h *HostAgent) run() {
	// 音声データ受信ループ
	go h.handleOpenAIMessages()

	// LiveKitメッセージハンドリングは不要（SDKが自動処理）

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
	if h.openaiConn == nil {
		// テストモード：テスト用の音声を生成
		h.generateTestAudio()
		return
	}

	log.Println("Starting OpenAI message handling loop...")
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

			log.Printf("Received OpenAI message: %+v", msg)

			// 音声出力をLiveKitにPublish
			if msgType, ok := msg["type"].(string); ok {
				switch msgType {
				case "response.output_audio.delta":
					if audioData, ok := msg["delta"].(string); ok {
						log.Printf("Received audio delta, length: %d", len(audioData))
						h.publishAudioToLiveKit(audioData)
					}
				case "response.done":
					log.Println("OpenAI response completed")
				case "session.created":
					log.Println("OpenAI session created")
				case "session.updated":
					log.Println("OpenAI session updated")
				case "conversation.item.added", "conversation.item.done", "response.output_audio.done", "response.output_audio_transcript.done", "response.content_part.done", "response.output_item.done", "rate_limits.updated":
					// これらのメッセージは無視
				default:
					log.Printf("Unhandled message type: %s", msgType)
				}
			}
		}
	}
}

func (h *HostAgent) sendMessage(content string) {
	if h.openaiConn == nil {
		log.Printf("Test mode: Message would be sent: %s", content)
		return
	}

	log.Printf("Sending message to OpenAI: %s", content)

	message := map[string]interface{}{
		"type": "conversation.item.create",
		"item": map[string]interface{}{
			"type": "message",
			"role": "user",
			"content": []map[string]interface{}{
				{
					"type": "input_text",
					"text": content,
				},
			},
		},
	}

	if err := h.openaiConn.WriteJSON(message); err != nil {
		log.Printf("Failed to send message: %v", err)
		return
	}

	log.Printf("Message sent successfully: %s", content)

	// 音声応答を強制的に生成
	responseCreate := map[string]interface{}{
		"type": "response.create",
	}

	if err := h.openaiConn.WriteJSON(responseCreate); err != nil {
		log.Printf("Failed to create response: %v", err)
		return
	}

	log.Printf("Response creation requested")
}

func (h *HostAgent) publishAudioToLiveKit(audioData string) {
	if h.audioTrack == nil {
		log.Println("Audio track not initialized, skipping audio publish")
		return
	}

	// テスト用の音声データかチェック
	if strings.HasPrefix(audioData, "test_audio_") {
		log.Printf("Test mode: Publishing test audio data: %s", audioData)
		// テスト用の音声データを生成（実際の音声ではなく、テスト用のデータ）
		testAudioBytes := generateTestAudioBytes(audioData)
		sample := media.Sample{
			Data:     testAudioBytes,
			Duration: 10 * time.Millisecond, // 10msのサンプル
		}

		if err := h.audioTrack.WriteSample(sample, nil); err != nil {
			log.Printf("Failed to write test audio sample: %v", err)
		}
		return
	}

	// 通常のBase64デコード
	audioBytes, err := base64.StdEncoding.DecodeString(audioData)
	if err != nil {
		log.Printf("Failed to decode audio data: %v", err)
		return
	}

	// 音声データをバッファに追加
	h.audioBuffer = append(h.audioBuffer, audioBytes...)

	// PCM16音声データを適切に処理（24kHz, 16bit = 2 bytes/sample）
	// 20msのチャンクに分割（24kHz * 20ms * 2 bytes = 960 bytes）
	chunkSize := 960 // 20ms分のデータ
	for len(h.audioBuffer) >= chunkSize {
		chunk := h.audioBuffer[:chunkSize]
		h.audioBuffer = h.audioBuffer[chunkSize:]

		// オーディオトラックに送信
		sample := media.Sample{
			Data:     chunk,
			Duration: 20 * time.Millisecond, // 20ms固定
		}
		if err := h.audioTrack.WriteSample(sample, nil); err != nil {
			log.Printf("Failed to write audio sample: %v", err)
			return
		}
	}

	log.Printf("Audio data published: %d bytes in chunks", len(audioBytes))
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

func (h *HostAgent) generateTestAudio() {
	log.Println("Starting test audio generation...")

	// テスト用の音声メッセージ
	messages := []string{
		"Radio-24、24時間放送中です。",
		"こんにちは、リスナーの皆さん。",
		"今日も素晴らしい一日をお過ごしください。",
		"音楽とお話でお楽しみいただいています。",
		"ご質問やご感想をお待ちしています。",
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	messageIndex := 0

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			if messageIndex < len(messages) {
				message := messages[messageIndex]
				log.Printf("Test mode: Sending message: %s", message)

				// テスト用の音声データを生成（実際の音声ではなく、テスト用のデータ）
				testAudioData := generateTestAudioData(message)
				h.publishAudioToLiveKit(testAudioData)

				messageIndex++
			} else {
				messageIndex = 0 // ループ
			}
		}
	}
}

func generateTestAudioData(text string) string {
	// テスト用の音声データを生成（実際の音声ではなく、テスト用のデータ）
	// 実際の実装では、ここでテキストを音声に変換する
	return fmt.Sprintf("test_audio_%s", text)
}

func generateTestAudioBytes(audioData string) []byte {
	// テスト用の音声バイトデータを生成（実際の音声ではなく、テスト用のデータ）
	// 実際の実装では、ここでテキストを音声に変換する
	// 10ms分のPCM16データを生成（48kHz, 16bit）- サイズを小さくする
	sampleRate := 48000
	duration := 10 // milliseconds
	samples := sampleRate * duration / 1000
	audioBytes := make([]byte, samples*2) // 16bit = 2bytes per sample

	// テスト用の音声波形を生成（サイン波）
	for i := 0; i < samples; i++ {
		// 440Hzのサイン波を生成
		amplitude := 32767 // 16bitの最大値
		frequency := 440.0
		time := float64(i) / float64(sampleRate)
		sample := int16(float64(amplitude) * 0.1 * math.Sin(2*math.Pi*frequency*time))

		// Little-endianでバイトに変換
		audioBytes[i*2] = byte(sample & 0xFF)
		audioBytes[i*2+1] = byte((sample >> 8) & 0xFF)
	}

	return audioBytes
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
