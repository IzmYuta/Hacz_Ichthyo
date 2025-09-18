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
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/livekit/media-sdk"
	"github.com/livekit/protocol/auth"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
)

type HostAgent struct {
	openaiConn       *websocket.Conn
	room             *lksdk.Room
	pcmTrack         *lkmedia.PCMLocalTrack
	pcmWriter        *PCMWriter
	reconnectTimer   *time.Timer
	ctx              context.Context
	cancel           context.CancelFunc
	currentPrompt    string
	scriptTopics     []string
	currentTopic     int
	dialogueMode     bool
	dialogueConn     *websocket.Conn
	audioPublication *lksdk.LocalTrackPublication
	// ユーザー音声用のトラック
	userPcmTrack         *lkmedia.PCMLocalTrack
	userPcmWriter        *PCMWriter
	userAudioPublication *lksdk.LocalTrackPublication
	// タイマーリセット用チャンネル
	timerResetChan chan struct{}
	// 状態管理用
	dialogueStateMutex sync.RWMutex
	dialogueStartedAt  time.Time
	lastActivity       time.Time
	// 対話モードタイムアウト用
	dialogueTimeoutChan chan struct{}
}

type PCMWriter struct {
	buf      []byte
	pcmTrack *lkmedia.PCMLocalTrack
	mu       sync.Mutex
}

const frameBytes = 960 // 20ms @ 24kHz mono 16-bit

func NewPCMWriter(t *lkmedia.PCMLocalTrack) *PCMWriter {
	return &PCMWriter{pcmTrack: t}
}

func (w *PCMWriter) WriteB64Delta(b64 string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

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

		if err := w.pcmTrack.WriteSample(pcm16Data); err != nil {
			log.Printf("Failed to write PCM16 sample: %v", err)
			return err
		}
		framesWritten++
	}
	log.Printf("WriteB64Delta completed: wrote %d frames, %d bytes remaining in buffer", framesWritten, len(w.buf))
	return nil
}

// ClearBuffer 音声バッファをクリア
func (w *PCMWriter) ClearBuffer() {
	w.mu.Lock()
	defer w.mu.Unlock()

	log.Printf("Clearing audio buffer (%d bytes)", len(w.buf))
	w.buf = w.buf[:0]
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
		ctx:    ctx,
		cancel: cancel,
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
		currentTopic:        0,
		dialogueMode:        false,
		timerResetChan:      make(chan struct{}, 10), // バッファを追加して複数の信号を処理可能にする
		dialogueTimeoutChan: make(chan struct{}, 1),  // 対話モードタイムアウト用
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

	// キュー監視ループを開始
	go agent.monitorQueue()

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
	h.audioPublication, err = h.room.LocalParticipant.PublishTrack(h.pcmTrack, &lksdk.TrackPublicationOptions{
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

// connectToOpenAIRealtime OpenAI Realtime API接続（対話モード用）
func (h *HostAgent) connectToOpenAIRealtime() error {
	apiKey := getEnv("OPENAI_API_KEY", "")
	if apiKey != "" {
		log.Printf("OpenAI API key: %s", apiKey[:10]+"...") // 最初の10文字だけ表示
	}

	if apiKey == "" || apiKey == "your-openai-api-key" || apiKey == "test-mode" {
		log.Println("OpenAI API key not set, using test mode")
		h.dialogueConn = nil // テストモード
		return nil
	}

	log.Println("Attempting to connect to OpenAI Realtime API for dialogue...")

	// OpenAI Realtime WebSocket接続
	url := "wss://api.openai.com/v1/realtime?model=gpt-realtime"
	headers := map[string][]string{
		"Authorization": {fmt.Sprintf("Bearer %s", apiKey)},
	}

	log.Printf("Connecting to: %s", url)
	conn, _, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		log.Printf("Failed to connect to OpenAI: %v, using test mode", err)
		h.dialogueConn = nil // テストモードにフォールバック
		return nil
	}

	h.dialogueConn = conn

	// セッション設定を送信
	if err := h.setupDialogueSession(); err != nil {
		log.Printf("Failed to setup dialogue session: %v", err)
		conn.Close()
		h.dialogueConn = nil
		return err
	}

	// 初回応答を待つ（セッション設定後の自動応答）
	if err := h.waitForInitialResponse(); err != nil {
		log.Printf("Failed to receive initial response: %v", err)
		// エラーでも続行（メッセージ処理ループは開始する）
	}

	// メッセージ処理ループを開始
	go h.handleDialogueMessages()

	log.Println("Connected to OpenAI Realtime for dialogue")
	return nil
}

// setupDialogueSession 対話用のセッション設定を送信
func (h *HostAgent) setupDialogueSession() error {
	if h.dialogueConn == nil {
		return fmt.Errorf("dialogue connection not available")
	}

	sessionUpdate := map[string]interface{}{
		"type": "session.update",
		"session": map[string]interface{}{
			"type":              "realtime",
			"instructions":      "あなたは24時間AIラジオのDJです。リスナーとの対話では、自然で親しみやすい口調で、ラジオDJらしい応答をしてください。短く、親しみやすく、エンターテイメント性のある会話を心がけてください。対話モードが開始されたら、まずは「こんにちは！ラジオ24のDJです。何かお話ししたいことはありますか？」のような挨拶をしてください。",
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

	if err := h.dialogueConn.WriteJSON(sessionUpdate); err != nil {
		return fmt.Errorf("failed to setup dialogue session: %w", err)
	}

	// セッション設定後に応答を促す（正しいAPI形式）
	responseRequest := map[string]interface{}{
		"type": "response.create",
	}

	if err := h.dialogueConn.WriteJSON(responseRequest); err != nil {
		log.Printf("Failed to request initial response: %v", err)
		// エラーでも続行
	} else {
		log.Println("Requested initial response from OpenAI")
	}

	log.Println("Dialogue session setup completed")
	return nil
}

// waitForInitialResponse 初回応答を待つ
func (h *HostAgent) waitForInitialResponse() error {
	if h.dialogueConn == nil {
		return fmt.Errorf("dialogue connection not available")
	}

	log.Println("Waiting for initial response from OpenAI Realtime...")

	// タイムアウト設定（10秒）
	timeout := time.After(10 * time.Second)

	for {
		select {
		case <-timeout:
			log.Println("Timeout waiting for initial response")
			return fmt.Errorf("timeout waiting for initial response")
		default:
			// メッセージを読み取り
			var msg map[string]interface{}
			if err := h.dialogueConn.ReadJSON(&msg); err != nil {
				log.Printf("Error reading initial response: %v", err)
				return err
			}

			log.Printf("Received initial message: %+v", msg)

			// メッセージタイプを確認
			if msgType, ok := msg["type"].(string); ok {
				switch msgType {
				case "response.output_audio.delta":
					// 初回応答音声を受信
					if audioData, ok := msg["delta"].(string); ok {
						log.Printf("Received initial audio response, length: %d", len(audioData))
						h.publishAudioToLiveKit(audioData)
					}
				case "response.content_part.done":
					// 初回応答のテキスト内容を字幕として送信
					if contentData, ok := msg["content_part"].(map[string]interface{}); ok {
						if content, ok := contentData["text"].(string); ok && content != "" {
							log.Printf("Received initial text content: %s", content)
							h.sendSubtitle(content)
						}
					}
				case "response.done":
					// 初回応答完了
					log.Println("Initial response completed")
					return nil
				case "session.created":
					log.Println("Session created, continuing to wait for response...")
					continue
				case "session.updated":
					log.Println("Session updated, continuing to wait for response...")
					continue
				case "conversation.item.added", "conversation.item.done", "response.output_audio.done", "response.output_audio_transcript.done", "response.output_item.done", "rate_limits.updated":
					// これらのメッセージは無視して続行
					continue
				case "error":
					if errorData, ok := msg["error"].(map[string]interface{}); ok {
						log.Printf("Error in initial response: %v", errorData)
						return fmt.Errorf("error in initial response: %v", errorData)
					}
				default:
					log.Printf("Unknown message type in initial response: %s", msgType)
					continue
				}
			}
		}
	}
}

// handleDialogueMessages 対話メッセージの処理
func (h *HostAgent) handleDialogueMessages() {
	if h.dialogueConn == nil {
		return
	}

	log.Println("Starting dialogue message handling loop...")
	for {
		select {
		case <-h.ctx.Done():
			log.Println("Context cancelled, stopping dialogue message handling")
			return
		default:
			var msg map[string]interface{}
			if err := h.dialogueConn.ReadJSON(&msg); err != nil {
				log.Printf("Dialogue connection error: %v", err)
				// 接続切断時に対話モードを終了
				log.Println("OpenAI Realtime connection lost, ending dialogue mode")
				h.endDialogueMode()
				return
			}

			log.Printf("Received dialogue message: %+v", msg)

			// 音声出力をLiveKitにPublish
			if msgType, ok := msg["type"].(string); ok {
				switch msgType {
				case "response.output_audio.delta":
					if audioData, ok := msg["delta"].(string); ok {
						log.Printf("Received dialogue audio delta, length: %d", len(audioData))
						h.publishAudioToLiveKit(audioData)

						// 音声出力時もアクティビティを更新
						h.dialogueStateMutex.Lock()
						if h.dialogueMode {
							h.lastActivity = time.Now()
							// タイムアウトタイマーをリセット
							h.resetDialogueTimeout()
						}
						h.dialogueStateMutex.Unlock()
					}
				case "response.content_part.done":
					// 対話中のテキスト応答が完了した時に字幕として送信
					if contentData, ok := msg["content_part"].(map[string]interface{}); ok {
						if content, ok := contentData["text"].(string); ok && content != "" {
							log.Printf("Received dialogue text content: %s", content)
							h.sendSubtitle(content)
						}
					}
				case "response.done":
					log.Println("Dialogue response completed")
				case "session.created":
					log.Println("Dialogue session created")
				case "session.updated":
					log.Println("Dialogue session updated")
				case "conversation.item.added", "conversation.item.done", "response.output_audio.done", "response.output_audio_transcript.done", "response.output_item.done", "rate_limits.updated":
					// これらのメッセージは無視
				case "error":
					if errorData, ok := msg["error"].(map[string]interface{}); ok {
						log.Printf("OpenAI Realtime error: %v", errorData)
						if code, ok := errorData["code"].(string); ok {
							switch code {
							case "input_audio_buffer_commit_empty":
								log.Println("Audio buffer is empty - this may be due to audio format issues")
							case "invalid_value":
								log.Println("Invalid audio data format - check PCM16 encoding")
							default:
								log.Printf("Unknown error code: %s", code)
							}
						}
					}
				default:
					log.Printf("Unhandled dialogue message type: %s", msgType)
				}
			}
		}
	}
}

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
			// 対話モードでない場合のみ台本を生成して発話
			if !h.dialogueMode {
				h.generateAndSpeakScript()
			}
		case <-h.timerResetChan:
			// LiveKitアップロード完了時にタイマーをリセット
			log.Println("Resetting timer due to LiveKit upload completion")
			ticker.Stop()
			ticker = time.NewTicker(30 * time.Second)
		}
	}
}

func (h *HostAgent) sendMessage(content string) {
	apiKey := getEnv("OPENAI_API_KEY", "")
	if apiKey == "" || apiKey == "your-openai-api-key" || apiKey == "test-mode" {
		log.Printf("Test mode: Message would be sent: %s", content)
		// テストモードでも字幕は送信
		h.sendSubtitle(content)
		return
	}

	log.Printf("Sending message to OpenAI TTS: %s", content)

	// 字幕を先に送信
	h.sendSubtitle(content)

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

	// LiveKitアップロード完了を通知（タイマーリセット用）
	select {
	case h.timerResetChan <- struct{}{}:
		log.Println("Timer reset signal sent - next script will be generated 30s after this upload")
	default:
		log.Println("Timer reset channel full, skipping signal")
	}
}

// sendSubtitle 字幕データをAPIサーバーに送信
func (h *HostAgent) sendSubtitle(text string) {
	apiBase := getEnv("API_BASE", "http://api:8080")

	payload := map[string]interface{}{
		"text": text,
		"type": "host_speech",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal subtitle data: %v", err)
		return
	}

	// HTTPクライアントにタイムアウトを設定
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(apiBase+"/v1/subtitle", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to send subtitle: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Subtitle API returned status: %d", resp.StatusCode)
		return
	}

	log.Printf("Subtitle sent successfully: %s", text)
}

// publishUserAudioToLiveKit ユーザー音声をLiveKitに送信
func (h *HostAgent) publishUserAudioToLiveKit(audioData string) {
	if h.userPcmWriter == nil {
		log.Println("User PCM writer not initialized, skipping user audio publish")
		return
	}

	log.Printf("publishUserAudioToLiveKit called with data length: %d", len(audioData))

	// ユーザー音声用PCMWriterを使用してBase64デルタデータを処理
	log.Printf("Processing user audio data via PCMWriter, calling WriteB64Delta...")
	if err := h.userPcmWriter.WriteB64Delta(audioData); err != nil {
		log.Printf("Failed to write user audio delta: %v", err)
		return
	}

	log.Printf("User audio data published via PCMWriter successfully")
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

func (h *HostAgent) startHTTPServer() {
	port := getEnv("HOST_PORT", "8080")

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
		prompt := fmt.Sprintf("%s トピック「%s」について、ラジオDJとして30秒程度の内容を話してください。必要に応じて最新の情報を検索して取り込んでください。", h.currentPrompt, req.Topic)
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

	// 対話モード用の音声入力エンドポイント
	http.HandleFunc("/audio/input", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Type  string `json:"type"`
			Audio string `json:"audio"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		log.Printf("Received audio input: %d bytes", len(req.Audio))

		// 対話モードのアクティビティを更新
		h.dialogueStateMutex.Lock()
		if h.dialogueMode {
			h.lastActivity = time.Now()
			// タイムアウトタイマーをリセット
			h.resetDialogueTimeout()
		}
		h.dialogueStateMutex.Unlock()

		// Base64デコードして実際のバイト数を確認
		if decoded, err := base64.StdEncoding.DecodeString(req.Audio); err == nil {
			log.Printf("Decoded audio data: %d bytes (PCM16 samples: %d)", len(decoded), len(decoded)/2)
			// 24kHzで100ms = 2400サンプル = 4800バイト
			durationMs := float64(len(decoded)/2) / 24000.0 * 1000.0
			log.Printf("Audio duration: %.2f ms", durationMs)

			// 25ms未満の場合は警告
			if durationMs < 25.0 {
				log.Printf("WARNING: Audio duration (%.2f ms) is less than 25ms, may cause buffer commit errors", durationMs)
			}
		} else {
			log.Printf("Failed to decode base64 audio data: %v", err)
		}

		// OpenAI Realtimeに音声データを送信
		if h.dialogueConn != nil {
			audioMessage := map[string]interface{}{
				"type":  "input_audio_buffer.append",
				"audio": req.Audio,
			}

			log.Printf("Sending audio to OpenAI Realtime: %d bytes, base64 length: %d", len(req.Audio), len(req.Audio))

			if err := h.dialogueConn.WriteJSON(audioMessage); err != nil {
				log.Printf("Failed to send audio to OpenAI: %v", err)
				http.Error(w, "Failed to send audio", http.StatusInternalServerError)
				return
			}

			log.Printf("Audio data sent successfully to OpenAI Realtime")
		} else {
			log.Printf("Dialogue connection is nil, cannot send audio")
		}

		// ユーザー音声をLiveKitに送信
		h.publishUserAudioToLiveKit(req.Audio)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "audio_sent",
		})
	})

	// 対話モード用の音声コミットエンドポイント
	http.HandleFunc("/audio/commit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		log.Printf("Received audio commit request")

		// OpenAI Realtimeにコミット信号を送信
		if h.dialogueConn != nil {
			commitMessage := map[string]interface{}{
				"type": "input_audio_buffer.commit",
			}
			if err := h.dialogueConn.WriteJSON(commitMessage); err != nil {
				log.Printf("Failed to send commit to OpenAI: %v", err)
				http.Error(w, "Failed to send commit", http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "commit_sent",
		})
	})

	// 対話終了エンドポイント
	http.HandleFunc("/dialogue/end", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		log.Printf("Received dialogue end request")

		// 対話モードを終了
		h.endDialogueMode()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "dialogue_ended",
		})
	})

	// 対話状態確認エンドポイント
	http.HandleFunc("/dialogue/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		h.dialogueStateMutex.RLock()
		active := h.dialogueMode
		startedAt := h.dialogueStartedAt
		lastActivity := h.lastActivity
		h.dialogueStateMutex.RUnlock()

		status := map[string]interface{}{
			"active":        active,
			"started_at":    startedAt.Format(time.RFC3339),
			"last_activity": lastActivity.Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
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

// monitorQueue キューを監視して対話リクエストを処理
func (h *HostAgent) monitorQueue() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	log.Println("Starting queue monitoring...")

	for {
		select {
		case <-h.ctx.Done():
			log.Println("Queue monitoring stopped")
			return
		case <-ticker.C:
			h.checkQueue()
		}
	}
}

// checkQueue キューをチェックして対話リクエストを処理
func (h *HostAgent) checkQueue() {
	// APIサーバーのキューエンドポイントをチェック
	apiBase := getEnv("API_BASE", "http://api:8080")

	// HTTPクライアントにタイムアウトを設定
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// まずヘルスチェックを実行
	healthResp, err := client.Get(apiBase + "/health")
	if err != nil {
		log.Printf("API server health check failed (server may not be running): %v", err)
		return
	}
	healthResp.Body.Close()

	if healthResp.StatusCode != http.StatusOK {
		log.Printf("API server health check returned status: %d", healthResp.StatusCode)
		return
	}

	resp, err := client.Get(apiBase + "/v1/queue/peek")
	if err != nil {
		log.Printf("Failed to check queue: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Queue peek returned status: %d", resp.StatusCode)
		return
	}

	var queueData struct {
		Item *struct {
			ID     string `json:"id"`
			UserID string `json:"user_id"`
			Kind   string `json:"kind"`
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"item"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&queueData); err != nil {
		log.Printf("Failed to decode queue response: %v", err)
		return
	}

	if queueData.Item != nil && queueData.Item.Kind == "dialogue" && queueData.Item.Status == "queued" {
		log.Printf("Dialogue request found in queue: %s", queueData.Item.ID)

		// クライアントIDを取得してからキューから削除
		clientID := queueData.Item.UserID
		log.Printf("Found client ID for dialogue request %s: %s", queueData.Item.ID, clientID)

		// キューからアイテムを削除して対話モードを開始
		h.dequeueDialogueRequest(queueData.Item.ID)
		h.startDialogueModeWithClientID(queueData.Item.ID, clientID)
	}
}

// startDialogueModeWithClientID クライアントIDを指定して対話モードを開始
func (h *HostAgent) startDialogueModeWithClientID(requestID, clientID string) {
	h.dialogueStateMutex.Lock()
	defer h.dialogueStateMutex.Unlock()

	if h.dialogueMode {
		log.Printf("Dialogue mode already active, ignoring request: %s", requestID)
		return // 既に対話モード中
	}

	log.Printf("Starting dialogue mode for request: %s, client: %s", requestID, clientID)
	h.dialogueMode = true
	h.dialogueStartedAt = time.Now()
	h.lastActivity = time.Now()

	// 3分のタイムアウトタイマーを開始
	go h.startDialogueTimeout()

	// 現在のTTSを停止（必要に応じて）
	log.Println("Stopping current TTS for dialogue mode")
	h.stopCurrentAudio()

	// 音声バッファをクリア
	if h.pcmWriter != nil {
		h.pcmWriter.ClearBuffer()
	}

	// 新しい音声トラックを作成（対話モード用）
	if err := h.createNewAudioTrack(); err != nil {
		log.Printf("Failed to create new audio track: %v", err)
		h.dialogueMode = false
		return
	}

	// ユーザー音声トラックを作成
	if err := h.createUserAudioTrack(); err != nil {
		log.Printf("Failed to create user audio track: %v", err)
		h.dialogueMode = false
		return
	}

	// OpenAI Realtime接続を開始
	if err := h.connectToOpenAIRealtime(); err != nil {
		log.Printf("Failed to connect to OpenAI Realtime: %v", err)
		h.dialogueMode = false
		return
	}

	// 対話開始の通知を送信（クライアントIDを含む）
	log.Printf("Sending dialogue_ready notification for request: %s, client: %s", requestID, clientID)
	h.sendDialogueNotificationWithClientID("dialogue_ready", requestID, clientID)
}

// startDialogueMode 対話モードを開始（後方互換性のため）
func (h *HostAgent) startDialogueMode(requestID string) {
	h.dialogueStateMutex.Lock()
	defer h.dialogueStateMutex.Unlock()

	if h.dialogueMode {
		log.Printf("Dialogue mode already active, ignoring request: %s", requestID)
		return // 既に対話モード中
	}

	log.Printf("Starting dialogue mode for request: %s", requestID)
	h.dialogueMode = true
	h.dialogueStartedAt = time.Now()
	h.lastActivity = time.Now()

	// 3分のタイムアウトタイマーを開始
	go h.startDialogueTimeout()

	// 現在のTTSを停止（必要に応じて）
	log.Println("Stopping current TTS for dialogue mode")
	h.stopCurrentAudio()

	// 音声バッファをクリア
	if h.pcmWriter != nil {
		h.pcmWriter.ClearBuffer()
	}

	// 新しい音声トラックを作成（対話モード用）
	if err := h.createNewAudioTrack(); err != nil {
		log.Printf("Failed to create new audio track: %v", err)
		h.dialogueMode = false
		return
	}

	// ユーザー音声トラックを作成
	if err := h.createUserAudioTrack(); err != nil {
		log.Printf("Failed to create user audio track: %v", err)
		h.dialogueMode = false
		return
	}

	// OpenAI Realtime接続を開始
	if err := h.connectToOpenAIRealtime(); err != nil {
		log.Printf("Failed to connect to OpenAI Realtime: %v", err)
		h.dialogueMode = false
		return
	}

	// 対話開始の通知を送信
	log.Printf("Sending dialogue_ready notification for request: %s", requestID)
	h.sendDialogueNotification("dialogue_ready", requestID)
}

// endDialogueMode 対話モードを終了
func (h *HostAgent) endDialogueMode() {
	h.dialogueStateMutex.Lock()
	defer h.dialogueStateMutex.Unlock()

	if !h.dialogueMode {
		log.Println("Dialogue mode not active, nothing to end")
		return
	}

	log.Println("Ending dialogue mode")
	h.dialogueMode = false

	// タイムアウトチャンネルをクリア
	select {
	case <-h.dialogueTimeoutChan:
		// チャンネルが既に空の場合は何もしない
	default:
		// チャンネルに何かある場合はクリア
	}
	// 新しい空のチャンネルを作成
	h.dialogueTimeoutChan = make(chan struct{}, 1)

	// OpenAI Realtime接続を切断
	if h.dialogueConn != nil {
		log.Println("Closing OpenAI Realtime connection")
		h.dialogueConn.Close()
		h.dialogueConn = nil
	}

	// 対話モード用の音声トラックを停止
	h.stopCurrentAudio()

	// ユーザー音声トラックを停止
	h.stopUserAudio()

	// 通常のラジオ放送用の音声トラックを作成
	if err := h.createNewAudioTrack(); err != nil {
		log.Printf("Failed to create normal audio track: %v", err)
	} else {
		log.Println("Normal audio track created")
	}

	// 通常のラジオ放送を再開
	log.Println("Resuming normal radio broadcast")
	h.sendMessage("ありがとうございました。通常のラジオ放送に戻ります。")

	// 対話終了の通知を送信
	log.Printf("Sending dialogue_ended notification")
	h.sendDialogueNotification("dialogue_ended", "")
}

// sendDialogueNotification 対話状態の変更を通知
func (h *HostAgent) sendDialogueNotification(notificationType, requestID string) {
	apiBase := getEnv("API_BASE", "http://api:8080")

	payload := map[string]interface{}{
		"type": notificationType,
	}
	if requestID != "" {
		payload["id"] = requestID
	}

	jsonData, _ := json.Marshal(payload)

	// HTTPクライアントにタイムアウトを設定
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(apiBase+"/v1/broadcast", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to send dialogue notification: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Dialogue notification sent: %s (requestID: %s)", notificationType, requestID)
}

// sendDialogueNotificationWithClientID クライアントIDを指定して対話状態の変更を通知
func (h *HostAgent) sendDialogueNotificationWithClientID(notificationType, requestID, clientID string) {
	apiBase := getEnv("API_BASE", "http://api:8080")

	payload := map[string]interface{}{
		"type": notificationType,
	}
	if requestID != "" {
		payload["id"] = requestID
	}
	if clientID != "" {
		payload["client_id"] = clientID
	}

	jsonData, _ := json.Marshal(payload)

	// HTTPクライアントにタイムアウトを設定
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(apiBase+"/v1/broadcast", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to send dialogue notification: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Dialogue notification sent: %s (requestID: %s, clientID: %s)", notificationType, requestID, clientID)
}

// dequeueDialogueRequest キューから対話リクエストを削除
func (h *HostAgent) dequeueDialogueRequest(requestID string) {
	apiBase := getEnv("API_BASE", "http://api:8080")

	payload := map[string]interface{}{
		"id": requestID,
	}

	jsonData, _ := json.Marshal(payload)

	// HTTPクライアントにタイムアウトを設定
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(apiBase+"/v1/queue/dequeue", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to dequeue dialogue request: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Dialogue request dequeued: %s", requestID)
}

// stopCurrentAudio 現在の音声トラックを停止・削除
func (h *HostAgent) stopCurrentAudio() {
	if h.audioPublication != nil {
		log.Println("Stopping and unpublishing current audio track")

		// トラックを停止
		if h.pcmTrack != nil {
			h.pcmTrack.Close()
		}

		// トラックをアンパブリッシュ
		err := h.room.LocalParticipant.UnpublishTrack(h.audioPublication.SID())
		if err != nil {
			log.Printf("Failed to unpublish track: %v", err)
		} else {
			log.Println("Audio track unpublished successfully")
		}

		h.audioPublication = nil
		h.pcmTrack = nil
		h.pcmWriter = nil
	}
}

// stopUserAudio ユーザー音声トラックを停止
func (h *HostAgent) stopUserAudio() {
	if h.userAudioPublication != nil {
		log.Println("Stopping and unpublishing user audio track")

		// ユーザートラックを停止
		if h.userPcmTrack != nil {
			h.userPcmTrack.Close()
		}

		// ユーザートラックをアンパブリッシュ
		err := h.room.LocalParticipant.UnpublishTrack(h.userAudioPublication.SID())
		if err != nil {
			log.Printf("Failed to unpublish user track: %v", err)
		} else {
			log.Println("User audio track unpublished successfully")
		}

		h.userAudioPublication = nil
		h.userPcmTrack = nil
		h.userPcmWriter = nil
	}
}

// createNewAudioTrack 新しい音声トラックを作成
func (h *HostAgent) createNewAudioTrack() error {
	log.Println("Creating new audio track for dialogue mode")

	// 新しいPCMオーディオトラックを作成
	var err error
	h.pcmTrack, err = lkmedia.NewPCMLocalTrack(24000, 1, nil) // 24kHz, mono
	if err != nil {
		return fmt.Errorf("failed to create new PCM audio track: %w", err)
	}

	// PCMWriterを初期化
	h.pcmWriter = NewPCMWriter(h.pcmTrack)

	// 新しいトラックをルームに公開
	h.audioPublication, err = h.room.LocalParticipant.PublishTrack(h.pcmTrack, &lksdk.TrackPublicationOptions{
		Name: "radio-24-host-dialogue",
	})
	if err != nil {
		return fmt.Errorf("failed to publish new track: %w", err)
	}

	log.Println("New audio track created and published successfully")
	return nil
}

// createUserAudioTrack ユーザー音声用のトラックを作成
func (h *HostAgent) createUserAudioTrack() error {
	log.Println("Creating user audio track")

	// ユーザー音声用のPCMオーディオトラックを作成
	var err error
	h.userPcmTrack, err = lkmedia.NewPCMLocalTrack(24000, 1, nil) // 24kHz, mono
	if err != nil {
		return fmt.Errorf("failed to create user PCM audio track: %w", err)
	}

	// ユーザー音声用PCMWriterを初期化
	h.userPcmWriter = NewPCMWriter(h.userPcmTrack)

	// ユーザー音声トラックをルームに公開
	h.userAudioPublication, err = h.room.LocalParticipant.PublishTrack(h.userPcmTrack, &lksdk.TrackPublicationOptions{
		Name: "radio-24-user-input",
	})
	if err != nil {
		return fmt.Errorf("failed to publish user track: %w", err)
	}

	log.Println("User audio track created and published successfully")
	return nil
}

// startDialogueTimeout 対話モードの3分タイムアウト処理
func (h *HostAgent) startDialogueTimeout() {
	log.Println("Starting dialogue timeout timer (3 minutes)")

	// 3分後にタイムアウトチャンネルに信号を送信
	time.AfterFunc(3*time.Minute, func() {
		select {
		case h.dialogueTimeoutChan <- struct{}{}:
			log.Println("Dialogue timeout signal sent")
		default:
			// チャンネルが満杯の場合は何もしない（既にタイムアウト処理が実行中）
		}
	})

	// タイムアウト信号を待機
	select {
	case <-h.dialogueTimeoutChan:
		// タイムアウトが発生した場合
		h.dialogueStateMutex.Lock()
		if h.dialogueMode {
			log.Println("Dialogue mode timeout (3 minutes) - ending dialogue")
			h.dialogueMode = false
			h.dialogueStateMutex.Unlock()

			// 対話モードを終了
			h.endDialogueMode()

			// タイムアウト通知を送信
			h.sendDialogueNotification("dialogue_timeout", "timeout")
		} else {
			h.dialogueStateMutex.Unlock()
		}
	case <-h.ctx.Done():
		// コンテキストがキャンセルされた場合
		log.Println("Dialogue timeout cancelled due to context cancellation")
	}
}

// resetDialogueTimeout 対話モードのタイムアウトタイマーをリセット
func (h *HostAgent) resetDialogueTimeout() {
	// タイムアウトチャンネルをクリアして新しいタイマーを開始
	select {
	case <-h.dialogueTimeoutChan:
		// 既存のタイムアウト信号をクリア
	default:
		// チャンネルが空の場合は何もしない
	}

	// 新しい3分タイマーを開始
	go func() {
		time.Sleep(3 * time.Minute)
		select {
		case h.dialogueTimeoutChan <- struct{}{}:
			log.Println("Dialogue timeout signal sent (after reset)")
		default:
			// チャンネルが満杯の場合は何もしない
		}
	}()
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
