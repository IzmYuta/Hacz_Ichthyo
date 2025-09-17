package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/radio24/director/pkg/director"
	"github.com/radio24/director/pkg/host"
)

var db *sql.DB
var programDirector *director.Director

func main() {
	// 環境変数読み込み
	err := godotenv.Load("../../.env")
	if err != nil {
		err = godotenv.Load("/app/.env")
		if err != nil {
			log.Println("No .env file found - using environment variables")
		} else {
			log.Println("Loaded .env file from /app/.env")
		}
	} else {
		log.Println("Loaded .env file from repository root")
	}

	// データベース接続
	if err := connectDatabase(); err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	// Program Director初期化
	hostClient := host.NewHostClient()
	programDirector = director.NewDirector(db, hostClient)
	programDirector.Start()

	// HTTPサーバー設定
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware())

	// ルート設定
	r.Get("/health", handleHealth)
	r.Get("/v1/now", handleNow)
	r.Post("/v1/admin/advance", handleAdvance)
	r.Post("/v1/admin/theme", handleThemeChange)
	r.Post("/v1/admin/prompt", handlePromptUpdate)
	r.Get("/v1/status", handleStatus)

	port := getEnv("PORT", "8081")
	log.Printf("Director Service starting on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

func connectDatabase() error {
	dbHost := getEnv("POSTGRES_HOST", "localhost")
	dbPort := getEnv("POSTGRES_PORT", "5432")
	dbUser := getEnv("POSTGRES_USER", "postgres")
	dbPassword := getEnv("POSTGRES_PASSWORD", "postgres")
	dbName := getEnv("POSTGRES_DB", "radio24")

	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName)

	var err error
	db, err = sql.Open("pgx", connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// データベース接続テスト
	if err = db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	log.Println("Database connected successfully")
	return nil
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
		"service":   "director",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func handleNow(w http.ResponseWriter, r *http.Request) {
	nowPlaying := programDirector.GetNowPlaying()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nowPlaying)
}

func handleAdvance(w http.ResponseWriter, r *http.Request) {
	programDirector.AdvanceSegment()
	nowPlaying := programDirector.GetNowPlaying()

	// Hostエージェントに指示を送信
	if err := programDirector.SendInstructionToHost("セグメントが進行しました。"); err != nil {
		log.Printf("Failed to send instruction to host: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nowPlaying)
}

func handleThemeChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Theme string `json:"theme"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	programDirector.SetTheme(req.Theme)
	nowPlaying := programDirector.GetNowPlaying()

	// Hostエージェントにテーマ変更を通知
	instruction := fmt.Sprintf("テーマが「%s」に変更されました。", req.Theme)
	if err := programDirector.SendInstructionToHost(instruction); err != nil {
		log.Printf("Failed to send theme change to host: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nowPlaying)
}

func handlePromptUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Hostエージェントのプロンプトを更新
	if err := programDirector.UpdateHostPrompt(req.Prompt); err != nil {
		log.Printf("Failed to update host prompt: %v", err)
		http.Error(w, "Failed to update prompt", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "updated",
	})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	status := programDirector.GetStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
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

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
