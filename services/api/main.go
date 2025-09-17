package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/radio24/api/internal/livekit"
	"github.com/radio24/api/pkg/broadcast"
	"github.com/radio24/api/pkg/queue"
)

type EphemeralResp struct {
	ClientSecret struct {
		Value     string `json:"value"`
		ExpiresAt int64  `json:"expires_at"`
	} `json:"client_secret"`
}

type Submission struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

type Theme struct {
	Title string `json:"title"`
	Color string `json:"color"`
}

var db *sql.DB
var tokenGenerator *livekit.TokenGenerator
var pttQueue *queue.Queue
var broadcastHub *broadcast.Hub
var dialogueConnections map[string]*websocket.Conn

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

	// DB接続
	dbHost := getEnv("POSTGRES_HOST", "localhost")
	dbPort := getEnv("POSTGRES_PORT", "5432")
	dbUser := getEnv("POSTGRES_USER", "postgres")
	dbPassword := getEnv("POSTGRES_PASSWORD", "postgres")
	dbName := getEnv("POSTGRES_DB", "radio24")

	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName)

	db, err = sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	// データベース接続テスト
	if err = db.Ping(); err != nil {
		log.Fatal("Failed to ping database:", err)
	}
	log.Println("Database connected successfully")

	// テーブル作成
	createTables()

	// LiveKit Token Generator初期化
	livekitAPIKey := getEnv("LIVEKIT_API_KEY", "devkey")
	livekitAPISecret := getEnv("LIVEKIT_API_SECRET", "secret")
	livekitURL := getEnv("LIVEKIT_URL", "ws://localhost:7880")
	tokenGenerator = livekit.NewTokenGenerator(livekitAPIKey, livekitAPISecret, livekitURL)

	// Broadcast Hub初期化
	broadcastHub = broadcast.NewHub()
	go broadcastHub.Run()

	// PTT Queue初期化
	pttQueue = queue.NewQueue()

	// 対話接続管理初期化
	dialogueConnections = make(map[string]*websocket.Conn)

	// ルーター設定
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware())

	// ルート
	r.Get("/health", handleHealth)
	r.Get("/ws/ptt", handlePTTWebSocket)
	r.Get("/ws/broadcast", handleBroadcastWebSocket)
	r.Post("/v1/realtime/ephemeral", handleEphemeral)
	r.Post("/v1/room/join", handleRoomJoin)
	r.Post("/v1/submission", handleSubmission)
	r.Post("/v1/theme/rotate", handleThemeRotate)
	r.Get("/v1/queue/peek", handleQueuePeek)
	r.Post("/v1/queue/dequeue", handleQueueDequeue)
	r.Post("/v1/broadcast", handleBroadcastMessage)

	port := getEnv("PORT", "8080")
	log.Printf("Server starting on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	// データベース接続確認
	if err := db.Ping(); err != nil {
		http.Error(w, "Database connection failed", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func handleRoomJoin(w http.ResponseWriter, r *http.Request) {
	var req livekit.JoinTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	if req.Identity == "" {
		http.Error(w, "Identity is required", 400)
		return
	}

	resp, err := tokenGenerator.GenerateJoinToken(req.Identity)
	if err != nil {
		log.Printf("Failed to generate join token: %v", err)
		http.Error(w, "Failed to generate token", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type PTTMessage struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type DialogueRequest struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // 本番では適切なオリジンチェックを実装
	},
}

func handlePTTWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	log.Println("PTT WebSocket connected")

	for {
		var msg map[string]interface{}
		err := conn.ReadJSON(&msg)
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}

		msgType, ok := msg["type"].(string)
		if !ok {
			log.Printf("Invalid message type")
			continue
		}

		switch msgType {
		case "ptt":
			// PTTアイテムをキューに追加
			kind, _ := msg["kind"].(string)
			text, _ := msg["text"].(string)

			item := queue.PTTItem{
				ID:       fmt.Sprintf("ptt_%d", time.Now().UnixNano()),
				UserID:   "anonymous", // 実際の実装では認証から取得
				Kind:     queue.PTTKind(kind),
				Text:     text,
				Priority: 0, // デフォルト優先度
			}

			pttQueue.Enqueue(item)
			log.Printf("PTT enqueued: %s", text)

			// クライアントに確認応答
			response := map[string]interface{}{
				"type": "ptt_queued",
				"id":   item.ID,
			}
			conn.WriteJSON(response)

		case "dialogue_request":
			// 対話リクエストをキューに追加
			kind, _ := msg["kind"].(string)

			item := queue.PTTItem{
				ID:       fmt.Sprintf("dialogue_%d", time.Now().UnixNano()),
				UserID:   "anonymous", // 実際の実装では認証から取得
				Kind:     queue.PTTKind(kind),
				Text:     "対話リクエスト",
				Priority: 10, // 対話リクエストは高優先度
			}

			pttQueue.Enqueue(item)
			log.Printf("Dialogue request enqueued: %s", item.ID)

			// クライアントに確認応答
			response := map[string]interface{}{
				"type": "dialogue_queued",
				"id":   item.ID,
			}
			conn.WriteJSON(response)

		case "dialogue_end":
			// 対話終了リクエスト
			log.Printf("Dialogue end request received")

			// hostエージェントに対話終了を通知
			endDialogueMode()

			// クライアントに確認応答
			response := map[string]interface{}{
				"type": "dialogue_end_ack",
			}
			conn.WriteJSON(response)

		case "input_audio_buffer.append":
			// 対話モード中の音声入力
			audio, _ := msg["audio"].(string)
			log.Printf("Received audio input for dialogue: %d bytes", len(audio))

			// OpenAI Realtimeに音声データを転送
			forwardAudioToOpenAI(audio)

		case "input_audio_buffer.commit":
			// 音声入力のコミット
			log.Printf("Audio input committed for dialogue")

			// OpenAI Realtimeにコミット信号を送信
			commitAudioToOpenAI()
		}
	}
}

func handleBroadcastWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Broadcast WebSocket upgrade failed: %v", err)
		return
	}

	// ユーザーIDを取得（実際の実装では認証から取得）
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = "anonymous"
	}

	broadcastHub.HandleWebSocket(conn, userID)
}

func corsMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := getEnv("ALLOWED_ORIGIN", "http://localhost:3000")
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func handleEphemeral(w http.ResponseWriter, r *http.Request) {
	// OpenAIの client_secrets を叩いて短命キーを発行
	payload := map[string]any{
		"session": map[string]any{
			"type": "realtime",
		},
	}
	b, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/realtime/client_secrets", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+os.Getenv("OPENAI_API_KEY"))
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	// 受け取った value のみをフロントへ返す（最小化）
	var parsed EphemeralResp
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.ClientSecret.Value == "" {
		// ドキュメント更新等でshapeが変わる可能性に備え原文も返す
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"client_secret": parsed.ClientSecret.Value,
		"expires_at":    parsed.ClientSecret.ExpiresAt,
	})
}

func handleSubmission(w http.ResponseWriter, r *http.Request) {
	var submission Submission
	if err := json.NewDecoder(r.Body).Decode(&submission); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	if submission.Text == "" {
		http.Error(w, "Text is required", 400)
		return
	}

	// OpenAI Embeddings API でベクトル化
	embedding, err := getEmbedding(submission.Text)
	if err != nil {
		log.Printf("Failed to get embedding: %v", err)
		http.Error(w, "Failed to process text", 500)
		return
	}

	// submission テーブルに保存
	var id string
	err = db.QueryRow(`
		INSERT INTO submission (user_id, type, text, embed, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id
	`, "anonymous", submission.Type, submission.Text, embedding).Scan(&id)

	if err != nil {
		log.Printf("Failed to save submission: %v", err)
		http.Error(w, "Failed to save submission", 500)
		return
	}

	// 類似投稿を3件検索して返す
	recommendations, err := getSimilarSubmissions(embedding, 3)
	if err != nil {
		log.Printf("Failed to get recommendations: %v", err)
		recommendations = []map[string]interface{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "saved",
		"id":              id,
		"text":            submission.Text,
		"recommendations": recommendations,
	})
}

func handleThemeRotate(w http.ResponseWriter, r *http.Request) {
	themes := []Theme{
		{Title: "深夜の音楽", Color: "#1a1a2e"},
		{Title: "朝のニュース", Color: "#16213e"},
		{Title: "午後のトーク", Color: "#0f3460"},
		{Title: "夜の物語", Color: "#533483"},
	}

	// 現在時刻に基づいてテーマを選択
	hour := time.Now().Hour()
	themeIndex := hour % len(themes)
	theme := themes[themeIndex]

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(theme)
}

func createTables() {
	// マイグレーション実行
	migrationSQL := `
	CREATE EXTENSION IF NOT EXISTS vector;

	CREATE TABLE IF NOT EXISTS submission (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id TEXT,
		type TEXT CHECK (type IN ('text','audio')) NOT NULL,
		text TEXT,
		embed VECTOR(1536),
		created_at TIMESTAMPTZ DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS submission_embed_hnsw
	ON submission USING hnsw (embed vector_cosine_ops);

	-- Program Director用テーブル
	CREATE TABLE IF NOT EXISTS channel (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name TEXT NOT NULL UNIQUE,
		live BOOLEAN DEFAULT true,
		started_at TIMESTAMPTZ DEFAULT now()
	);

	CREATE TABLE IF NOT EXISTS schedule (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		channel_id UUID REFERENCES channel(id) ON DELETE CASCADE,
		hour INTEGER CHECK (hour >= 0 AND hour <= 23),
		block TEXT CHECK (block IN ('OP', 'NEWS', 'QANDA', 'MUSIC', 'TOPIC_A', 'JINGLE')) NOT NULL,
		prompt TEXT,
		created_at TIMESTAMPTZ DEFAULT now()
	);

	CREATE TABLE IF NOT EXISTS queue (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id TEXT,
		kind TEXT CHECK (kind IN ('audio', 'text', 'phone')) NOT NULL,
		text TEXT,
		meta JSONB,
		enqueued_at TIMESTAMPTZ DEFAULT now(),
		status TEXT CHECK (status IN ('queued', 'live', 'done', 'dropped')) DEFAULT 'queued'
	);

	-- デフォルトチャンネルを作成
	INSERT INTO channel (name, live) VALUES ('Radio-24', true) ON CONFLICT (name) DO NOTHING;

	-- デフォルトスケジュールを作成（24時間分）
	INSERT INTO schedule (channel_id, hour, block, prompt) 
	SELECT 
		c.id,
		h.hour,
		CASE 
			WHEN h.hour BETWEEN 0 AND 5 THEN 'MUSIC'
			WHEN h.hour BETWEEN 6 AND 8 THEN 'NEWS'
			WHEN h.hour BETWEEN 9 AND 11 THEN 'TOPIC_A'
			WHEN h.hour BETWEEN 12 AND 14 THEN 'QANDA'
			WHEN h.hour BETWEEN 15 AND 17 THEN 'MUSIC'
			WHEN h.hour BETWEEN 18 AND 20 THEN 'NEWS'
			WHEN h.hour BETWEEN 21 AND 23 THEN 'TOPIC_A'
		END as block,
		CASE 
			WHEN h.hour BETWEEN 0 AND 5 THEN '深夜の音楽を流しながら、静かに語りかけましょう。'
			WHEN h.hour BETWEEN 6 AND 8 THEN '朝のニュースを分かりやすく伝え、一日の始まりを応援しましょう。'
			WHEN h.hour BETWEEN 9 AND 11 THEN '午前のトピックについて、リスナーと一緒に考えましょう。'
			WHEN h.hour BETWEEN 12 AND 14 THEN 'リスナーからの質問に答える時間です。'
			WHEN h.hour BETWEEN 15 AND 17 THEN '午後の音楽でリラックスした時間を提供しましょう。'
			WHEN h.hour BETWEEN 18 AND 20 THEN '夕方のニュースで一日を振り返りましょう。'
			WHEN h.hour BETWEEN 21 AND 23 THEN '夜のトピックで深く語り合いましょう。'
		END as prompt
	FROM channel c
	CROSS JOIN (
		SELECT generate_series(0, 23) as hour
	) h
	WHERE c.name = 'Radio-24'
	ON CONFLICT DO NOTHING;

	-- インデックス作成
	CREATE INDEX IF NOT EXISTS idx_schedule_channel_hour ON schedule(channel_id, hour);
	CREATE INDEX IF NOT EXISTS idx_queue_status_enqueued ON queue(status, enqueued_at);
	CREATE INDEX IF NOT EXISTS idx_queue_meta_priority ON queue USING GIN (meta);
	`

	_, err := db.Exec(migrationSQL)
	if err != nil {
		log.Printf("Migration error: %v", err)
	}
}

func getEmbedding(text string) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	payload := map[string]interface{}{
		"model": "text-embedding-3-small",
		"input": text,
	}

	jsonData, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/embeddings", bytes.NewBuffer(jsonData))
	req.Header.Set("Authorization", "Bearer "+apiKey)
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
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Data) == 0 {
		return "", fmt.Errorf("no embedding returned")
	}

	// ベクトルを文字列に変換（pgvector形式）
	embedding := result.Data[0].Embedding
	embeddingStr := "["
	for i, val := range embedding {
		if i > 0 {
			embeddingStr += ","
		}
		embeddingStr += fmt.Sprintf("%f", val)
	}
	embeddingStr += "]"

	return embeddingStr, nil
}

func getSimilarSubmissions(queryEmbedding string, limit int) ([]map[string]interface{}, error) {
	rows, err := db.Query(`
		SELECT id, text, created_at, 1 - (embed <=> $1) as similarity
		FROM submission
		WHERE embed IS NOT NULL
		ORDER BY embed <=> $1
		LIMIT $2
	`, queryEmbedding, limit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recommendations []map[string]interface{}
	for rows.Next() {
		var id, text, createdAt string
		var similarity float64

		if err := rows.Scan(&id, &text, &createdAt, &similarity); err != nil {
			continue
		}

		recommendations = append(recommendations, map[string]interface{}{
			"id":         id,
			"text":       text,
			"created_at": createdAt,
			"similarity": similarity,
		})
	}

	return recommendations, nil
}

func handleQueuePeek(w http.ResponseWriter, r *http.Request) {
	item := pttQueue.Peek()

	w.Header().Set("Content-Type", "application/json")
	if item != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"item": item,
		})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"item": nil,
		})
	}
}

func handleQueueDequeue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// キューからアイテムを削除
	removed := pttQueue.Remove(req.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"removed": removed,
		"id":      req.ID,
	})
}

func handleBroadcastMessage(w http.ResponseWriter, r *http.Request) {
	var msg map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	msgType := msg["type"].(string)
	log.Printf("Broadcasting message: %s", msgType)

	// Broadcast Hubにメッセージを送信
	broadcastHub.Broadcast(msgType, msg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "broadcasted",
	})
}

// forwardAudioToOpenAI OpenAI Realtimeに音声データを転送
func forwardAudioToOpenAI(audioData string) {
	// 実際の実装では、hostエージェントのOpenAI Realtime接続に音声データを転送
	// ここでは簡略化のため、hostエージェントにHTTPリクエストで通知
	apiBase := getEnv("HOST_BASE", "http://host:8080")

	payload := map[string]interface{}{
		"type":  "audio_input",
		"audio": audioData,
	}

	jsonData, _ := json.Marshal(payload)

	// HTTPクライアントにタイムアウトを設定
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(apiBase+"/audio/input", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to forward audio to OpenAI: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Audio forwarded to OpenAI Realtime")
}

// commitAudioToOpenAI OpenAI Realtimeに音声コミット信号を送信
func commitAudioToOpenAI() {
	// 実際の実装では、hostエージェントのOpenAI Realtime接続にコミット信号を転送
	apiBase := getEnv("HOST_BASE", "http://host:8080")

	payload := map[string]interface{}{
		"type": "audio_commit",
	}

	jsonData, _ := json.Marshal(payload)

	// HTTPクライアントにタイムアウトを設定
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(apiBase+"/audio/commit", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to commit audio to OpenAI: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Audio commit signal sent to OpenAI Realtime")
}

// endDialogueMode hostエージェントに対話終了を通知
func endDialogueMode() {
	apiBase := getEnv("HOST_BASE", "http://host:8080")

	payload := map[string]interface{}{
		"type": "end_dialogue",
	}

	jsonData, _ := json.Marshal(payload)

	// HTTPクライアントにタイムアウトを設定
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(apiBase+"/dialogue/end", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to notify host agent to end dialogue: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Dialogue end notification sent to host agent")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
