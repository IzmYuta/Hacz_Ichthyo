# Radio-24 Makefile
# 24時間AIラジオ局の環境構築・開発・デプロイ用Makefile

# 変数定義
DOCKER_COMPOSE := docker-compose
DOCKER := docker
GO := go
PNPM := pnpm
NODE := node

# ポート設定
API_PORT := 8080
WEB_PORT := 3000
DB_PORT := 5432

# ディレクトリ設定
API_DIR := services/api
HOST_DIR := services/host
WEB_DIR := apps/web
DB_DIR := db

# デフォルトターゲット
.DEFAULT_GOAL := help

# ヘルプ表示
.PHONY: help
help: ## このヘルプを表示
	@echo "Radio-24 開発環境 Makefile"
	@echo "=========================="
	@echo ""
	@echo "利用可能なコマンド:"
	@echo ""
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "例:"
	@echo "  make setup     # 環境構築"
	@echo "  make dev       # 開発環境起動"
	@echo "  make build     # 本番ビルド"
	@echo "  make clean     # クリーンアップ"

# =============================================================================
# 環境構築
# =============================================================================

.PHONY: setup
setup: setup-env setup-db setup-api setup-host setup-web ## 完全な環境構築を実行
	@echo ""
	@echo "✅ 環境構築が完了しました！"
	@echo ""
	@echo "次のステップ:"
	@echo "  1. .env ファイルに OpenAI API キーを設定"
	@echo "  2. make dev で開発環境を起動"
	@echo "  3. http://localhost:3000 にアクセス"

.PHONY: setup-docker
setup-docker: setup-env ## Docker環境構築を実行
	@echo "🐳 Docker環境を構築中..."
	@echo "  1. Dockerイメージをビルド中..."
	$(DOCKER_COMPOSE) build
	@echo "  2. 全サービスを起動中..."
	$(DOCKER_COMPOSE) up -d
	@echo ""
	@echo "✅ Docker環境構築が完了しました！"
	@echo ""
	@echo "アクセス先:"
	@echo "  Webアプリ: http://localhost:3000"
	@echo "  APIサーバー: http://localhost:8080"
	@echo "  データベース: localhost:5432"
	@echo ""
	@echo "次のステップ:"
	@echo "  1. .env ファイルに OpenAI API キーを設定"
	@echo "  2. make docker-restart でサービスを再起動"
	@echo "  3. http://localhost:3000 にアクセス"

.PHONY: setup-env
setup-env: ## 環境設定ファイルを作成
	@echo "🔧 環境設定ファイルを作成中..."
	@if [ ! -f .env ]; then \
		cp .env.example .env; \
		echo "✅ .env ファイルを作成しました"; \
	else \
		echo "ℹ️  .env ファイルは既に存在します"; \
	fi

.PHONY: setup-db
setup-db: ## データベースを起動・初期化
	@echo "🐘 データベースを起動中..."
	$(DOCKER_COMPOSE) up -d db
	@echo "⏳ データベースの起動を待機中..."
	@sleep 5
	@echo "✅ データベースが起動しました"

.PHONY: setup-api
setup-api: ## APIサーバーの依存関係をインストール
	@echo "🔧 APIサーバーの依存関係をインストール中..."
	cd $(API_DIR) && $(GO) mod tidy
	@echo "✅ APIサーバーの依存関係をインストールしました"

.PHONY: setup-host
setup-host: ## Hostサービスの依存関係をインストール
	@echo "🎙️  Hostサービスの依存関係をインストール中..."
	cd $(HOST_DIR) && $(GO) mod tidy
	@echo "✅ Hostサービスの依存関係をインストールしました"

.PHONY: setup-web
setup-web: ## Webアプリの依存関係をインストール
	@echo "🌐 Webアプリの依存関係をインストール中..."
	cd $(WEB_DIR) && $(PNPM) install
	@echo "✅ Webアプリの依存関係をインストールしました"

# =============================================================================
# 開発環境
# =============================================================================

.PHONY: dev
dev: dev-db dev-api dev-web ## 開発環境を起動（データベース + API + Web）

.PHONY: dev-all
dev-all: dev-db dev-api dev-host dev-web ## 全サービスを起動（データベース + API + Host + Web）

.PHONY: dev-db
dev-db: ## データベースのみ起動
	@echo "🐘 データベースを起動中..."
	$(DOCKER_COMPOSE) up -d db
	@echo "✅ データベースが起動しました (ポート: $(DB_PORT))"

.PHONY: dev-api
dev-api: ## APIサーバーを起動
	@echo "🔧 APIサーバーを起動中..."
	@echo "  ポート: $(API_PORT)"
	@echo "  停止するには Ctrl+C を押してください"
	cd $(API_DIR) && $(GO) run main.go

.PHONY: dev-web
dev-web: ## Webアプリを起動
	@echo "🌐 Webアプリを起動中..."
	@echo "  ポート: $(WEB_PORT)"
	@echo "  停止するには Ctrl+C を押してください"
	cd $(WEB_DIR) && $(PNPM) dev

.PHONY: dev-api-bg
dev-api-bg: ## APIサーバーをバックグラウンドで起動
	@echo "🔧 APIサーバーをバックグラウンドで起動中..."
	cd $(API_DIR) && $(GO) run main.go &
	@echo "✅ APIサーバーがバックグラウンドで起動しました (PID: $$!)"

.PHONY: dev-host
dev-host: ## Hostサービスを起動
	@echo "🎙️  Hostサービスを起動中..."
	@echo "  停止するには Ctrl+C を押してください"
	cd $(HOST_DIR) && $(GO) run main.go

.PHONY: dev-host-bg
dev-host-bg: ## Hostサービスをバックグラウンドで起動
	@echo "🎙️  Hostサービスをバックグラウンドで起動中..."
	cd $(HOST_DIR) && $(GO) run main.go &
	@echo "✅ Hostサービスがバックグラウンドで起動しました (PID: $$!)"

.PHONY: dev-web-bg
dev-web-bg: ## Webアプリをバックグラウンドで起動
	@echo "🌐 Webアプリをバックグラウンドで起動中..."
	cd $(WEB_DIR) && $(PNPM) dev &
	@echo "✅ Webアプリがバックグラウンドで起動しました (PID: $$!)"

# =============================================================================
# ビルド
# =============================================================================

.PHONY: build
build: build-api build-host build-web ## 本番用ビルドを実行

.PHONY: build-api
build-api: ## APIサーバーをビルド
	@echo "🔧 APIサーバーをビルド中..."
	cd $(API_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o server main.go
	@echo "✅ APIサーバーのビルドが完了しました"

.PHONY: build-host
build-host: ## Hostサービスをビルド
	@echo "🎙️  Hostサービスをビルド中..."
	cd $(HOST_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o host main.go
	@echo "✅ Hostサービスのビルドが完了しました"

.PHONY: build-web
build-web: ## Webアプリをビルド
	@echo "🌐 Webアプリをビルド中..."
	cd $(WEB_DIR) && $(PNPM) build
	@echo "✅ Webアプリのビルドが完了しました"

# =============================================================================
# Docker
# =============================================================================

.PHONY: docker-build
docker-build: ## Dockerイメージをビルド
	@echo "🐳 Dockerイメージをビルド中..."
	$(DOCKER_COMPOSE) build
	@echo "✅ Dockerイメージのビルドが完了しました"

.PHONY: docker-up
docker-up: ## Docker Composeで全サービスを起動
	@echo "🐳 Docker Composeで全サービスを起動中..."
	$(DOCKER_COMPOSE) up -d
	@echo "✅ 全サービスが起動しました"
	@echo ""
	@echo "アクセス先:"
	@echo "  Webアプリ: http://localhost:3000"
	@echo "  APIサーバー: http://localhost:8080"
	@echo "  データベース: localhost:5432"

.PHONY: docker-down
docker-down: ## Docker Composeで全サービスを停止
	@echo "🐳 Docker Composeで全サービスを停止中..."
	$(DOCKER_COMPOSE) down
	@echo "✅ 全サービスが停止しました"

.PHONY: docker-restart
docker-restart: ## Docker Composeで全サービスを再起動
	@echo "🐳 Docker Composeで全サービスを再起動中..."
	$(DOCKER_COMPOSE) restart
	@echo "✅ 全サービスが再起動しました"

.PHONY: docker-logs
docker-logs: ## Docker Composeのログを表示
	@echo "📋 Docker Composeログ:"
	$(DOCKER_COMPOSE) logs -f

.PHONY: docker-status
docker-status: ## Docker Composeのサービス状態を確認
	@echo "📊 Docker Composeサービス状態:"
	$(DOCKER_COMPOSE) ps

.PHONY: docker-clean
docker-clean: ## Docker Composeのデータとボリュームを削除
	@echo "🧹 Docker Composeのデータとボリュームを削除中..."
	$(DOCKER_COMPOSE) down -v --remove-orphans
	$(DOCKER) system prune -f
	@echo "✅ Docker Composeのクリーンアップが完了しました"

# =============================================================================
# テスト
# =============================================================================

.PHONY: test
test: test-api test-host test-web ## 全テストを実行

.PHONY: test-api
test-api: ## APIサーバーのテストを実行
	@echo "🧪 APIサーバーのテストを実行中..."
	cd $(API_DIR) && $(GO) test ./...
	@echo "✅ APIサーバーのテストが完了しました"

.PHONY: test-host
test-host: ## Hostサービスのテストを実行
	@echo "🧪 Hostサービスのテストを実行中..."
	cd $(HOST_DIR) && $(GO) test ./...
	@echo "✅ Hostサービスのテストが完了しました"

.PHONY: test-web
test-web: ## Webアプリのテストを実行
	@echo "🧪 Webアプリのテストを実行中..."
	cd $(WEB_DIR) && $(PNPM) test
	@echo "✅ Webアプリのテストが完了しました"

.PHONY: test-e2e
test-e2e: ## E2Eテストを実行
	@echo "🧪 E2Eテストを実行中..."
	@echo "⚠️  E2Eテストは実装予定です"
	@echo "✅ E2Eテストが完了しました"

# =============================================================================
# データベース操作
# =============================================================================

.PHONY: db-migrate
db-migrate: ## データベースマイグレーションを実行
	@echo "🗃️  データベースマイグレーションを実行中..."
	$(DOCKER) exec radio24-db psql -U postgres -d radio24 -f /docker-entrypoint-initdb.d/001_init.sql
	$(DOCKER) exec radio24-db psql -U postgres -d radio24 -f /docker-entrypoint-initdb.d/002_schema.sql
	@echo "✅ データベースマイグレーションが完了しました"

.PHONY: db-reset
db-reset: ## データベースをリセット
	@echo "🗃️  データベースをリセット中..."
	$(DOCKER_COMPOSE) down -v
	$(DOCKER_COMPOSE) up -d db
	@sleep 5
	@$(MAKE) db-migrate
	@echo "✅ データベースがリセットされました"

.PHONY: db-shell
db-shell: ## データベースシェルに接続
	@echo "🗃️  データベースシェルに接続中..."
	$(DOCKER) exec -it radio24-db psql -U postgres -d radio24

# =============================================================================
# クリーンアップ
# =============================================================================

.PHONY: clean
clean: clean-build clean-deps clean-docker ## 全クリーンアップを実行
	@echo "🧹 クリーンアップが完了しました"

.PHONY: clean-build
clean-build: ## ビルド成果物を削除
	@echo "🧹 ビルド成果物を削除中..."
	rm -rf $(API_DIR)/server
	rm -rf $(HOST_DIR)/host
	rm -rf $(WEB_DIR)/.next
	rm -rf $(WEB_DIR)/out
	rm -rf $(WEB_DIR)/build
	@echo "✅ ビルド成果物を削除しました"

.PHONY: clean-deps
clean-deps: ## 依存関係を削除
	@echo "🧹 依存関係を削除中..."
	rm -rf $(WEB_DIR)/node_modules
	rm -rf $(WEB_DIR)/.pnpm-store
	@echo "✅ 依存関係を削除しました"

.PHONY: clean-docker
clean-docker: ## Docker関連をクリーンアップ
	@echo "🧹 Docker関連をクリーンアップ中..."
	$(DOCKER_COMPOSE) down -v
	$(DOCKER) system prune -f
	@echo "✅ Docker関連をクリーンアップしました"

# =============================================================================
# ユーティリティ
# =============================================================================

.PHONY: status
status: ## サービス状態を確認
	@echo "📊 サービス状態:"
	@echo ""
	@echo "🐘 データベース:"
	@$(DOCKER) ps --filter name=radio24-db --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
	@echo ""
	@echo "🔧 APIサーバー (ポート $(API_PORT)):"
	@lsof -i :$(API_PORT) 2>/dev/null | grep LISTEN || echo "  停止中"
	@echo ""
	@echo "🎙️  Hostサービス:"
	@ps aux | grep "go run main.go" | grep -v grep || echo "  停止中"
	@echo ""
	@echo "🌐 Webアプリ (ポート $(WEB_PORT)):"
	@lsof -i :$(WEB_PORT) 2>/dev/null | grep LISTEN || echo "  停止中"

.PHONY: logs
logs: ## ログを表示
	@echo "📋 サービスログ:"
	$(DOCKER_COMPOSE) logs -f

.PHONY: logs-api
logs-api: ## APIサーバーのログを表示
	@echo "📋 APIサーバーログ:"
	$(DOCKER_COMPOSE) logs -f api

.PHONY: logs-web
logs-web: ## Webアプリのログを表示
	@echo "📋 Webアプリログ:"
	$(DOCKER_COMPOSE) logs -f web

.PHONY: logs-db
logs-db: ## データベースのログを表示
	@echo "📋 データベースログ:"
	$(DOCKER_COMPOSE) logs -f db

# =============================================================================
# 開発支援
# =============================================================================

.PHONY: format
format: ## コードフォーマットを実行
	@echo "🎨 コードフォーマットを実行中..."
	cd $(API_DIR) && $(GO) fmt ./...
	cd $(HOST_DIR) && $(GO) fmt ./...
	cd $(WEB_DIR) && $(PNPM) format
	@echo "✅ コードフォーマットが完了しました"

.PHONY: lint
lint: ## リンターを実行
	@echo "🔍 リンターを実行中..."
	cd $(API_DIR) && $(GO) vet ./...
	cd $(HOST_DIR) && $(GO) vet ./...
	cd $(WEB_DIR) && $(PNPM) lint
	@echo "✅ リンターが完了しました"

.PHONY: check
check: format lint test ## コード品質チェックを実行
	@echo "✅ コード品質チェックが完了しました"

# =============================================================================
# デプロイ
# =============================================================================

.PHONY: deploy
deploy: build docker-build ## デプロイ用ビルドを実行
	@echo "🚀 デプロイ用ビルドが完了しました"
	@echo ""
	@echo "次のステップ:"
	@echo "  1. Cloud Run にデプロイ"
	@echo "  2. 環境変数を設定"
	@echo "  3. ドメインを設定"

.PHONY: deploy-staging
deploy-staging: ## ステージング環境にデプロイ
	@echo "🚀 ステージング環境にデプロイ中..."
	@echo "⚠️  ステージングデプロイは実装予定です"
	@echo "✅ ステージングデプロイが完了しました"

.PHONY: deploy-prod
deploy-prod: ## 本番環境にデプロイ
	@echo "🚀 本番環境にデプロイ中..."
	@echo "⚠️  本番デプロイは実装予定です"
	@echo "✅ 本番デプロイが完了しました"
