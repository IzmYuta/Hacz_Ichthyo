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
	"github.com/radio24/api/pkg/director"
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
var programDirector *director.Director
var pttQueue *queue.Queue

func main() {
	// 環境変数読み込み
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found")
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
	tokenGenerator = livekit.NewTokenGenerator(livekitAPIKey, livekitAPISecret)

	// Program Director初期化
	hostChannel := make(chan string, 100)
	programDirector = director.NewDirector(hostChannel)
	programDirector.Start()

	// PTT Queue初期化
	pttQueue = queue.NewQueue()

	// ルーター設定
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware())

	// ルート
	r.Get("/health", handleHealth)
	r.Get("/v1/now", handleNow)
	r.Post("/v1/admin/advance", handleAdvance)
	r.Get("/ws/ptt", handlePTTWebSocket)
	r.Post("/v1/realtime/ephemeral", handleEphemeral)
	r.Post("/v1/room/join", handleRoomJoin)
	r.Post("/v1/submission", handleSubmission)
	r.Post("/v1/theme/rotate", handleThemeRotate)

	port := getEnv("API_PORT", "8080")
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

func handleNow(w http.ResponseWriter, r *http.Request) {
	nowPlaying := programDirector.GetNowPlaying()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nowPlaying)
}

func handleAdvance(w http.ResponseWriter, r *http.Request) {
	programDirector.AdvanceSegment()
	nowPlaying := programDirector.GetNowPlaying()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nowPlaying)
}

type PTTMessage struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
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
		var msg PTTMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}

		if msg.Type == "ptt" {
			// PTTアイテムをキューに追加
			item := queue.PTTItem{
				ID:       fmt.Sprintf("ptt_%d", time.Now().UnixNano()),
				UserID:   "anonymous", // 実際の実装では認証から取得
				Kind:     queue.PTTKind(msg.Kind),
				Text:     msg.Text,
				Priority: 0, // デフォルト優先度
			}

			pttQueue.Enqueue(item)
			log.Printf("PTT enqueued: %s", msg.Text)

			// クライアントに確認応答
			response := map[string]interface{}{
				"type": "ptt_queued",
				"id":   item.ID,
			}
			conn.WriteJSON(response)
		}
	}
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

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
