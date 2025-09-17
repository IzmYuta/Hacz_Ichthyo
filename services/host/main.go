package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
	currentPrompt  string
	scriptTopics   []string
	currentTopic   int
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

type ScriptRequest struct {
	Topic string `json:"topic"`
	Style string `json:"style"`
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
		ctx:           ctx,
		cancel:        cancel,
		scriptTopics: []string{
			"今日の天気予報",
			"最新のニュース",
			"音楽の話題",
			"リスナーからのメッセージ",
			"今日の出来事",
			"季節の話題",
			"テクノロジーの話題",
			"エンターテイメント",
		},
		currentTopic: 0,
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

	// OpenAI Realtime接続（将来のPTT実装用にコメントアウト）
	// if err := agent.connectToOpenAI(); err != nil {
	// 	log.Printf("Failed to connect to OpenAI: %v", err)
	// 	// OpenAI接続に失敗しても続行（テストモードで動作）
	// }

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

// connectToOpenAI OpenAI Realtime API接続（将来のPTT実装用にコメントアウト）
// func (h *HostAgent) connectToOpenAI() error {
// 	apiKey := getEnv("OPENAI_API_KEY", "")
// 	if apiKey != "" {
// 		log.Printf("OpenAI API key: %s", apiKey[:10]+"...") // 最初の10文字だけ表示
// 	}

// 	if apiKey == "" || apiKey == "your-openai-api-key" || apiKey == "test-mode" {
// 		log.Println("OpenAI API key not set, using test mode")
// 		h.openaiConn = nil // テストモード
// 		return nil
// 	}

// 	log.Println("Attempting to connect to OpenAI Realtime API...")

// 	// OpenAI Realtime WebSocket接続
// 	url := "wss://api.openai.com/v1/realtime?model=gpt-realtime"
// 	headers := map[string][]string{
// 		"Authorization": {fmt.Sprintf("Bearer %s", apiKey)},
// 	}

// 	log.Printf("Connecting to: %s", url)
// 	conn, _, err := websocket.DefaultDialer.Dial(url, headers)
// 	if err != nil {
// 		log.Printf("Failed to connect to OpenAI: %v, using test mode", err)
// 		h.openaiConn = nil // テストモードにフォールバック
// 		return nil
// 	}

// 	h.openaiConn = conn

// 	log.Println("Connected to OpenAI Realtime")
// 	return nil
// }

func (h *HostAgent) run() {
	// 音声データ受信ループ（将来のPTT実装用にコメントアウト）
	// go h.handleOpenAIMessages()

	// LiveKitメッセージハンドリングは不要（SDKが自動処理）

	// 定期発話ループ（30秒ごと）
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			// 台本を生成して発話
			h.generateAndSpeakScript()
		}
	}
}

// handleOpenAIMessages OpenAI Realtimeメッセージ処理（将来のPTT実装用にコメントアウト）
// func (h *HostAgent) handleOpenAIMessages() {
// 	if h.openaiConn == nil {
// 		return
// 	}

// 	log.Println("Starting OpenAI message handling loop...")
// 	for {
// 		select {
// 		case <-h.ctx.Done():
// 			return
// 		default:
// 			var msg map[string]interface{}
// 			if err := h.openaiConn.ReadJSON(&msg); err != nil {
// 				log.Printf("OpenAI connection error: %v", err)
// 				h.reconnectOpenAI()
// 				return
// 			}

// 			log.Printf("Received OpenAI message: %+v", msg)

// 			// 音声出力をLiveKitにPublish
// 			if msgType, ok := msg["type"].(string); ok {
// 				switch msgType {
// 				case "response.output_audio.delta":
// 					if audioData, ok := msg["delta"].(string); ok {
// 						log.Printf("Received audio delta, length: %d", len(audioData))
// 						h.publishAudioToLiveKit(audioData)
// 					}
// 				case "response.done":
// 					log.Println("OpenAI response completed")
// 				case "session.created":
// 					log.Println("OpenAI session created")
// 				case "session.updated":
// 					log.Println("OpenAI session updated")
// 				case "conversation.item.added", "conversation.item.done", "response.output_audio.done", "response.output_audio_transcript.done", "response.content_part.done", "response.output_item.done", "rate_limits.updated":
// 					// これらのメッセージは無視
// 				default:
// 					log.Printf("Unhandled message type: %s", msgType)
// 				}
// 			}
// 		}
// 	}
// }

func (h *HostAgent) sendMessage(content string) {
	apiKey := getEnv("OPENAI_API_KEY", "")
	if apiKey == "" || apiKey == "your-openai-api-key" || apiKey == "test-mode" {
		log.Printf("Test mode: Message would be sent: %s", content)
		return
	}

	log.Printf("Sending message to OpenAI TTS: %s", content)

	// OpenAI TTS APIを使用して音声を生成
	audioData, err := h.generateTTS(content, apiKey)
	if err != nil {
		log.Printf("Failed to generate TTS: %v", err)
		return
	}

	// 生成された音声をLiveKitに送信
	h.publishAudioToLiveKit(audioData)
	log.Printf("TTS audio generated and published successfully")
}

// generateTTS OpenAI TTS APIを使用してテキストを音声に変換
func (h *HostAgent) generateTTS(text, apiKey string) (string, error) {
	url := "https://api.openai.com/v1/audio/speech"

	requestBody := map[string]interface{}{
		"model":           "tts-1",
		"input":           text,
		"voice":           "nova",
		"response_format": "pcm",
		"speed":           1.0,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("TTS API error: %d - %s", resp.StatusCode, string(body))
	}

	audioBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// PCMデータをBase64エンコード
	audioBase64 := base64.StdEncoding.EncodeToString(audioBytes)
	log.Printf("Generated TTS audio: %d bytes, base64 length: %d", len(audioBytes), len(audioBase64))

	return audioBase64, nil
}

func (h *HostAgent) publishAudioToLiveKit(audioData string) {
	if h.pcmWriter == nil {
		log.Println("PCM writer not initialized, skipping audio publish")
		return
	}

	log.Printf("publishAudioToLiveKit called with data length: %d", len(audioData))

	// PCMWriterを使用してBase64デルタデータを処理
	log.Printf("Processing real audio data via PCMWriter, calling WriteB64Delta...")
	if err := h.pcmWriter.WriteB64Delta(audioData); err != nil {
		log.Printf("Failed to write audio delta: %v", err)
		return
	}

	log.Printf("Audio data published via PCMWriter successfully")
}

// generateScript OpenAI APIを使用して台本を生成
func (h *HostAgent) generateScript(prompt string) (string, error) {
	apiKey := getEnv("OPENAI_API_KEY", "")
	if apiKey == "" || apiKey == "your-openai-api-key" || apiKey == "test-mode" {
		log.Println("OpenAI API key not set, using test mode")
		return "テストモードです。ラジオ24をお聞きいただき、ありがとうございます。", nil
	}

	url := "https://api.openai.com/v1/chat/completions"

	requestBody := map[string]interface{}{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "あなたは24時間AIラジオのDJです。自然で親しみやすい口調で、リスナーとの距離感を大切にしてください。",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"max_tokens":  200,
		"temperature": 0.8,
		"top_p":       0.9,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error: %d - %s", resp.StatusCode, string(body))
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}

	return response.Choices[0].Message.Content, nil
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

// reconnectOpenAI OpenAI Realtime再接続（将来のPTT実装用にコメントアウト）
// func (h *HostAgent) reconnectOpenAI() {
// 	if h.reconnectTimer != nil {
// 		h.reconnectTimer.Stop()
// 	}

// 	h.reconnectTimer = time.AfterFunc(5*time.Second, func() {
// 		log.Println("Attempting to reconnect to OpenAI...")
// 		if err := h.connectToOpenAI(); err != nil {
// 			log.Printf("Reconnection failed: %v", err)
// 			h.reconnectOpenAI() // 再試行
// 		} else {
// 			log.Println("Reconnected to OpenAI")
// 		}
// 	})
// }

// generateAndSpeakScript 台本を生成してTTSで読み上げ
func (h *HostAgent) generateAndSpeakScript() {
	// 現在のトピックを取得
	topic := h.scriptTopics[h.currentTopic]
	h.currentTopic = (h.currentTopic + 1) % len(h.scriptTopics)

	// 台本生成用のプロンプトを作成
	prompt := fmt.Sprintf("%s トピック「%s」について、ラジオDJとして30秒程度の内容を話してください。自然で親しみやすい口調で、リスナーとの距離感を大切にしてください。", h.currentPrompt, topic)

	log.Printf("Generating script for topic: %s", topic)

	// OpenAI APIを使用して台本を生成
	script, err := h.generateScript(prompt)
	if err != nil {
		log.Printf("Failed to generate script: %v", err)
		// フォールバック用の簡単なメッセージ
		script = fmt.Sprintf("こんにちは、ラジオ24です。%sについてお話しします。", topic)
	}

	log.Printf("Generated script: %s", script)

	// 生成された台本をTTSで読み上げ
	h.sendMessage(script)
}

// updateOpenAIPrompt OpenAIセッションのプロンプトを更新（将来のPTT実装用にコメントアウト）
// func (h *HostAgent) updateOpenAIPrompt(newPrompt string) {
// 	if h.openaiConn == nil {
// 		log.Println("OpenAI connection not available, skipping prompt update")
// 		return
// 	}

// 	log.Printf("Updating OpenAI prompt: %s", newPrompt)

// 	sessionUpdate := map[string]interface{}{
// 		"type": "session.update",
// 		"session": map[string]interface{}{
// 			"type":              "realtime",
// 			"instructions":      newPrompt,
// 			"output_modalities": []string{"audio"},
// 			"audio": map[string]interface{}{
// 				"input": map[string]interface{}{
// 					"turn_detection": map[string]interface{}{
// 						"type":            "server_vad",
// 						"idle_timeout_ms": 6000,
// 					},
// 				},
// 				"output": map[string]interface{}{
// 					"voice": "marin",
// 				},
// 			},
// 		},
// 	}

// 	if err := h.openaiConn.WriteJSON(sessionUpdate); err != nil {
// 		log.Printf("Failed to update OpenAI prompt: %v", err)
// 		return
// 	}

// 	log.Println("OpenAI prompt updated successfully")
// }

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

	// 台本生成エンドポイント
	http.HandleFunc("/script/generate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req ScriptRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		log.Printf("Received script generation request: topic=%s, style=%s", req.Topic, req.Style)

		// プロンプトを作成
		prompt := fmt.Sprintf("%s トピック「%s」について、ラジオDJとして30秒程度の内容を話してください。", h.currentPrompt, req.Topic)
		if req.Style != "" {
			prompt += fmt.Sprintf(" スタイル: %s", req.Style)
		}

		// 台本を生成
		script, err := h.generateScript(prompt)
		if err != nil {
			log.Printf("Failed to generate script: %v", err)
			http.Error(w, "Failed to generate script", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"script": script,
		})
	})

	// 即座に発話するエンドポイント
	http.HandleFunc("/speak", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Text string `json:"text"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		log.Printf("Received speak request: %s", req.Text)

		// 即座に発話
		h.sendMessage(req.Text)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "speaking",
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
