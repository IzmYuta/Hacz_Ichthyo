# インフラ構成ドキュメント

## 概要

24時間AIラジオシステムのインフラ構成について説明します。このシステムはGoogle Cloud Platform (GCP)上で動作し、マイクロサービスアーキテクチャを採用しています。

## アーキテクチャ概要（放送型）

```mermaid
graph TB
    subgraph "Client Layer"
        WEB[Next.js Web App<br/>Subscribe Only]
        MOBILE[Mobile App<br/>PWA Subscribe Only]
    end
    
    subgraph "GCP Cloud Run Services"
        API[API Service<br/>Go<br/>PTT Queue Management]
        HOST[Host Service<br/>Go<br/>24h Continuous Broadcast]
        LIVEKIT[LiveKit Service<br/>WebRTC SFU<br/>Audio Distribution]
    end
    
    subgraph "Data Layer"
        DB[(Cloud SQL<br/>PostgreSQL + pgvector<br/>CHANNEL/SCHEDULE/QUEUE)]
        REDIS[(Redis<br/>Cache & Queue<br/>PTT Queue)]
    end
    
    subgraph "External Services"
        OPENAI[OpenAI Realtime API<br/>Single Session]
        STORAGE[Cloud Storage<br/>Backups & Media<br/>Recordings & Clips]
    end
    
    
    %% Client connections (Subscribe Only)
    WEB --> LIVEKIT
    MOBILE --> LIVEKIT
    WEB --> API
    MOBILE --> API
    
    %% Service connections
    API --> DB
    API --> REDIS
    API --> LIVEKIT
    HOST --> LIVEKIT
    HOST --> OPENAI
    LIVEKIT --> REDIS
    
    %% PTT Flow
    WEB -.->|PTT Audio/Text| API
    API -.->|Queue Management| REDIS
    
    %% Data connections
    API --> STORAGE
    HOST --> STORAGE
```

## サービス構成

### 1. API Service (Go)

- **役割**: メインのAPIサーバー
- **ポート**: 8080
- **リソース**: 1Gi RAM, 1 CPU
- **最大インスタンス**: 10
- **機能**:
  - ユーザー認証・管理
  - 投稿の受付・管理
  - 番組進行の制御
  - LiveKitトークンの発行
  - PTT WebSocket接続の管理
  - Broadcast WebSocket接続の管理
  - 対話状態管理
  - キュー管理（優先度制御）

### 2. Host Service (Go)

- **役割**: AI DJの常時発話サービス（放送型の中核）
- **ポート**: 8080
- **リソース**: 1Gi RAM, 1 CPU
- **最大インスタンス**: 1 (常時起動、固定)
- **機能**:
  - OpenAI Realtime APIとの常時接続（単一セッション）
  - 24時間連続音声生成・配信
  - LiveKitへの音声配信（Publish）
  - PCM音声処理・音量調整
  - 対話モード（OpenAI Realtime API）
  - 音声ミキシング（ホスト音声 + ユーザー音声）
  - キュー監視・対話リクエスト処理
  - テストモード対応（OpenAI接続失敗時）
  - 自動再接続機能

### 3. LiveKit Service (WebRTC SFU)

- **役割**: リアルタイム音声配信の中継
- **ポート**: 7880 (HTTP), 7881 (UDP)
- **リソース**: 2Gi RAM, 2 CPU
- **最大インスタンス**: 3
- **機能**:
  - WebRTC音声ストリーミング
  - 複数ユーザーの同時接続
  - 音声ミキシング・ダッキング
  - 録音・クリップ生成

### 4. Web Service (Next.js)

- **役割**: フロントエンドアプリケーション（放送型対応）
- **ポート**: 3000
- **リソース**: 512Mi RAM, 1 CPU
- **最大インスタンス**: 5
- **機能**:
  - ユーザーインターフェース
  - LiveKit接続（Subscribe Only）
  - PTT (Push-to-Talk) 機能
  - 対話モード（AI DJとのリアルタイム対話）
  - 音声録音・送信（WebM→PCM16変換）
  - 投稿・コメント機能
  - 番組進行情報表示
  - テーマ切替UI
  - リアルタイム音声処理

## データベース構成

### Cloud SQL (PostgreSQL)

- **バージョン**: PostgreSQL 15
- **インスタンス**: db-f1-micro
- **ストレージ**: 10GB SSD (自動拡張)
- **リージョン**: asia-northeast1
- **拡張機能**: pgvector (ベクトル検索用)

#### テーブル構成

```sql
-- 投稿管理
CREATE TABLE submission (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT,
    type TEXT CHECK (type IN ('text', 'audio')) NOT NULL,
    text TEXT,
    embed VECTOR(1536),  -- OpenAI埋め込みベクトル
    created_at TIMESTAMPTZ DEFAULT now()
);

-- ベクトル検索用インデックス
CREATE INDEX submission_embed_hnsw ON submission 
USING hnsw (embed vector_cosine_ops);

-- チャンネル管理
CREATE TABLE channel (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    live BOOLEAN DEFAULT true,
    started_at TIMESTAMPTZ DEFAULT now()
);

-- スケジュール管理
CREATE TABLE schedule (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID REFERENCES channel(id) ON DELETE CASCADE,
    hour INTEGER CHECK (hour >= 0 AND hour <= 23),
    block TEXT CHECK (block IN ('OP', 'NEWS', 'QANDA', 'MUSIC', 'TOPIC_A', 'JINGLE')) NOT NULL,
    prompt TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- キュー管理
CREATE TABLE queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT,
    kind TEXT CHECK (kind IN ('audio', 'text', 'phone')) NOT NULL,
    text TEXT,
    meta JSONB,
    enqueued_at TIMESTAMPTZ DEFAULT now(),
    status TEXT CHECK (status IN ('queued', 'live', 'done', 'dropped')) DEFAULT 'queued'
);

-- インデックス
CREATE INDEX idx_schedule_channel_hour ON schedule(channel_id, hour);
CREATE INDEX idx_queue_status_enqueued ON queue(status, enqueued_at);
CREATE INDEX idx_queue_meta_priority ON queue USING GIN (meta);
```

### Redis

- **バージョン**: Redis 7.0
- **サイズ**: 1GB
- **リージョン**: asia-northeast1
- **用途**:
  - セッション管理
  - キャッシュ
  - キュー管理
  - リアルタイムデータ

## ネットワーク構成

### VPC Connector

- **名前**: radio24-connector
- **リージョン**: asia-northeast1
- **最小インスタンス**: 2
- **最大インスタンス**: 3
- **用途**: Cloud RunとVPC内リソースの接続

### セキュリティ

- **認証**: Cloud Run IAM
- **HTTPS**: 全サービスでHTTPS強制
- **CORS**: 適切なオリジン設定
- **シークレット管理**: GitHub Secrets + Cloud Secret Manager

## デプロイメント構成

### デプロイメント方法

- **プラットフォーム**: Cloud Build
- **レジストリ**: Google Container Registry
- **デプロイ戦略**: 直接デプロイ
- **テスト**: ローカルテスト

### 環境変数

```bash
# API Service
POSTGRES_HOST=radio24-db
POSTGRES_PORT=5432
POSTGRES_USER=radio24-user
POSTGRES_PASSWORD=***
POSTGRES_DB=radio24
OPENAI_API_KEY=***
LIVEKIT_API_KEY=***
LIVEKIT_API_SECRET=***

# Host Service
LIVEKIT_API_KEY=***
LIVEKIT_API_SECRET=***
OPENAI_API_KEY=***
LIVEKIT_WS_URL=wss://livekit-***.run.app

# Web Service
NEXT_PUBLIC_API_BASE=https://api-***.run.app
NEXT_PUBLIC_OPENAI_REALTIME_MODEL=gpt-realtime
```

## 運用・保守

### バックアップ

- **データベース**: 日次自動バックアップ
- **ストレージ**: Cloud Storageへのエクスポート
- **保持期間**: 30日間

### セキュリティ

- **シークレット管理**: Cloud Secret Manager
- **アクセス制御**: IAMによる権限管理
- **ネットワーク**: VPC内での通信

## スケーリング戦略

### 水平スケーリング

- **API Service**: 負荷に応じて0-10インスタンス
- **Web Service**: 負荷に応じて0-5インスタンス
- **LiveKit Service**: 負荷に応じて0-3インスタンス

### 垂直スケーリング

- **Host Service**: 常時1インスタンス（固定）
- **データベース**: 必要に応じてインスタンスサイズ変更

## コスト最適化

### リソース最適化

- **最小インスタンス**: 不要なサービスは0に設定
- **CPU割り当て**: 用途に応じた適切な割り当て
- **メモリ使用量**: 効率的なメモリ管理

### コスト管理

- **リソース最適化**: 適切なインスタンスサイズ設定
- **未使用リソース**: 定期的なクリーンアップ

## 災害復旧

### バックアップ戦略

- **データベース**: 日次バックアップ + ポイントインタイム復旧
- **設定**: Infrastructure as Code
- **アプリケーション**: コンテナイメージのバージョン管理

### 復旧手順

1. バックアップからのデータベース復旧
2. 最新のコンテナイメージでのサービス再デプロイ
3. 設定の復元と検証

## セキュリティ考慮事項

### データ保護

- **暗号化**: 転送時・保存時の暗号化
- **アクセス制御**: IAMによる細かい権限管理
- **監査ログ**: 全操作のログ記録

### コンプライアンス

- **GDPR**: 個人データの適切な処理
- **セキュリティ**: 定期的なセキュリティ監査
- **可用性**: 99.9%の可用性目標

## 運用ガイド

### デプロイメント

1. ローカルでのテスト実行
2. Cloud Buildでのコンテナイメージビルド
3. Cloud Runへのデプロイ
4. ヘルスチェックの確認

### トラブルシューティング

- **ログ確認**: Cloud Loggingでの詳細ログ
- **ヘルスチェック**: 各サービスのヘルスエンドポイント確認
- **ロールバック**: 前バージョンへの迅速な復旧

### メンテナンス

- **定期更新**: 依存関係の手動更新
- **セキュリティパッチ**: 緊急パッチの適用
- **パフォーマンス**: 定期的な最適化

## 今後の拡張計画

### 機能拡張

- **マルチリージョン**: 複数リージョンでの展開
- **CDN**: Cloud CDNの導入
- **AI機能**: より高度なAI機能の追加

### 技術改善

- **マイクロサービス**: さらなるサービス分割
- **イベント駆動**: イベント駆動アーキテクチャの採用
- **リアルタイム**: より低遅延の実現
