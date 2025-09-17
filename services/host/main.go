package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/livekit/media-sdk"
	"github.com/livekit/protocol/auth"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
)

type HostAgent struct {
	openaiConn     *websocket.Conn
	room           *lksdk.Room
	pcmTrack       *lkmedia.PCMLocalTrack
	pcmWriter      *PCMWriter
	reconnectTimer *time.Timer
	ctx            context.Context
	cancel         context.CancelFunc
}

type PCMWriter struct {
	buf      []byte
	pcmTrack *lkmedia.PCMLocalTrack
}

const frameBytes = 960 // 20ms @ 24kHz mono 16-bit

func NewPCMWriter(t *lkmedia.PCMLocalTrack) *PCMWriter {
	return &PCMWriter{pcmTrack: t}
}

func (w *PCMWriter) WriteB64Delta(b64 string) error {
	log.Printf("WriteB64Delta called with base64 length: %d", len(b64))

	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		log.Printf("Failed to decode base64 audio data: %v", err)
		return err
	}

	log.Printf("Decoded audio data: %d bytes", len(raw))
	w.buf = append(w.buf, raw...)
	log.Printf("Buffer now contains: %d bytes", len(w.buf))

	framesWritten := 0
	for len(w.buf) >= frameBytes {
		frame := w.buf[:frameBytes]
		w.buf = w.buf[frameBytes:]

		// 音量を下げる（-6dB）
		attenuateInPlace(frame, 0.5)

		// PCM16データをPCMLocalTrackに送信
		// frameは[]byteなので、[]int16に変換
		pcm16Data := make(media.PCM16Sample, len(frame)/2)
		for i := 0; i < len(frame); i += 2 {
			pcm16Data[i/2] = int16(int(frame[i]) | int(frame[i+1])<<8)
		}

		log.Printf("Writing PCM16 sample %d: %d samples to PCMLocalTrack", framesWritten+1, len(pcm16Data))
		if err := w.pcmTrack.WriteSample(pcm16Data); err != nil {
			log.Printf("Failed to write PCM16 sample: %v", err)
			return err
		}
		log.Printf("Successfully wrote PCM16 sample %d", framesWritten+1)
		framesWritten++
	}
	log.Printf("WriteB64Delta completed: wrote %d frames, %d bytes remaining in buffer", framesWritten, len(w.buf))
	return nil
}

func attenuateInPlace(b []byte, gain float64) {
	// b は little-endian の int16 PCM
	for i := 0; i+1 < len(b); i += 2 {
		sample := int16(int(b[i]) | int(b[i+1])<<8)
		v := float64(sample) * gain
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		s := int16(v)
		b[i] = byte(s)
		b[i+1] = byte(uint16(s) >> 8)
	}
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

	// HTTPサーバーを起動（Cloud Run用）
	agent.startHTTPServer()

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
		log.Printf("Failed to connect to OpenAI: %v", err)
		// OpenAI接続に失敗しても続行（テストモードで動作）
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

	// PCMオーディオトラックを作成（24kHz, mono）
	log.Println("Creating PCM audio track...")
	h.pcmTrack, err = lkmedia.NewPCMLocalTrack(24000, 1, nil) // 24kHz, mono
	if err != nil {
		return fmt.Errorf("failed to create PCM audio track: %w", err)
	}

	// PCMWriterを初期化
	h.pcmWriter = NewPCMWriter(h.pcmTrack)

	// トラックをルームに公開
	log.Println("Publishing PCM audio track to room...")
	_, err = h.room.LocalParticipant.PublishTrack(h.pcmTrack, &lksdk.TrackPublicationOptions{
		Name: "radio-24-host",
	})
	if err != nil {
		return fmt.Errorf("failed to publish track: %w", err)
	}

	log.Println("Successfully connected to LiveKit room and published audio track")

	// 接続確認のためのログ
	log.Printf("LiveKit room state: connected=%v", h.room != nil)
	log.Printf("PCM track state: track=%v, writer=%v", h.pcmTrack != nil, h.pcmWriter != nil)

	return nil
}

func (h *HostAgent) connectToOpenAI() error {
	apiKey := getEnv("OPENAI_API_KEY", "")
	if apiKey != "" {
		log.Printf("OpenAI API key: %s", apiKey[:10]+"...") // 最初の10文字だけ表示
	}

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
	if h.pcmWriter == nil {
		log.Println("PCM writer not initialized, skipping audio publish")
		return
	}

	log.Printf("publishAudioToLiveKit called with data length: %d", len(audioData))

	// テスト用の音声データかチェック
	if strings.HasPrefix(audioData, "test_audio_") {
		log.Printf("Test mode: Publishing test audio data: %s", audioData)
		// テスト用の音声データを生成（実際の音声ではなく、テスト用のデータ）
		testAudioBytes := generateTestAudioBytes(audioData)
		log.Printf("Generated test audio bytes: %d bytes", len(testAudioBytes))

		// テストデータをBase64エンコードしてPCMWriterに送信
		testAudioB64 := base64.StdEncoding.EncodeToString(testAudioBytes)
		log.Printf("Test audio base64 length: %d", len(testAudioB64))
		if err := h.pcmWriter.WriteB64Delta(testAudioB64); err != nil {
			log.Printf("Failed to write test audio sample: %v", err)
		} else {
			log.Printf("Successfully published test audio")
		}
		return
	}

	// PCMWriterを使用してBase64デルタデータを処理
	log.Printf("Processing real audio data via PCMWriter, calling WriteB64Delta...")
	if err := h.pcmWriter.WriteB64Delta(audioData); err != nil {
		log.Printf("Failed to write audio delta: %v", err)
		return
	}

	log.Printf("Audio data published via PCMWriter successfully")
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
	// 20ms分のPCM16データを生成（24kHz, 16bit）- 960バイト
	sampleRate := 24000
	duration := 20 // milliseconds
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

func (h *HostAgent) startHTTPServer() {
	port := getEnv("PORT", "8080")

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":    "healthy",
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	log.Printf("Starting HTTP server on port %s", port)
	go func() {
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()
	
	// HTTPサーバーの起動を少し待つ
	time.Sleep(1 * time.Second)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
