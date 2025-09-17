# Program Director 実装

## 概要

Program Directorは、24時間ラジオの番組進行を自動管理するシステムです。仕様書v2に基づいて実装され、以下の機能を提供します。

## 主要機能

### 1. 番組進行ループ

- **テーマ切替**: 毎正時にテーマを自動切替
- **セグメント管理**: 15分刻みでセグメント進行（OP → TOPIC_A → QANDA → JINGLE → NEWS）
- **スケジュール管理**: データベースから時間帯別の進行ガイダンスを読み込み

### 2. データベース統合

- **CHANNEL**: チャンネル管理（将来のマルチチャンネル対応）
- **SCHEDULE**: 時間帯別の番組スケジュールとプロンプト
- **QUEUE**: PTT投稿の優先度と状態管理

### 3. MCP（Model Context Protocol）機能

- **天気情報**: 外部APIから天気情報を取得
- **ニュース情報**: 最新ニュースの取得
- **FAQ情報**: よくある質問の検索
- **文脈情報生成**: OpenAI APIを使用した現在の状況に適した話題生成

### 4. Host Agent連携

- **プロンプト更新**: 現在の状況に基づいてHost Agent用プロンプトを動的生成
- **キュー情報**: PTT投稿キュー情報をプロンプトに反映
- **進行ガイダンス**: データベースから読み込んだ進行ガイダンスを適用

### 5. UI連動（WebSocket配信）

- **リアルタイム配信**: 番組状態の変更をリアルタイムで配信
- **リスナー数管理**: 接続中のリスナー数を追跡
- **キュー更新通知**: PTT投稿の追加・更新を即座に配信

## アーキテクチャ

```
Director
├── Schedule Management (データベース連携)
├── Theme Management (時間帯別テーマ切替)
├── Segment Management (15分刻み進行)
├── MCP Client (外部情報取得)
├── Host Channel (Host Agent連携)
└── Broadcast Hub (WebSocket配信)
```

## API エンドポイント

- `GET /v1/now`: 現在の番組情報取得
- `POST /v1/admin/advance`: 手動セグメント進行
- `GET /ws/broadcast`: WebSocket配信接続

## データベーススキーマ

### CHANNEL

```sql
CREATE TABLE channel (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    live BOOLEAN DEFAULT true,
    started_at TIMESTAMPTZ DEFAULT now()
);
```

### SCHEDULE

```sql
CREATE TABLE schedule (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID REFERENCES channel(id) ON DELETE CASCADE,
    hour INTEGER CHECK (hour >= 0 AND hour <= 23),
    block TEXT CHECK (block IN ('OP', 'NEWS', 'QANDA', 'MUSIC', 'TOPIC_A', 'JINGLE')) NOT NULL,
    prompt TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);
```

### QUEUE

```sql
CREATE TABLE queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT,
    kind TEXT CHECK (kind IN ('audio', 'text', 'phone')) NOT NULL,
    text TEXT,
    meta JSONB,
    enqueued_at TIMESTAMPTZ DEFAULT now(),
    status TEXT CHECK (status IN ('queued', 'live', 'done', 'dropped')) DEFAULT 'queued'
);
```

## 設定

環境変数:

- `OPENAI_API_KEY`: OpenAI APIキー（MCP機能用）
- `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`: データベース接続情報

## 使用方法

1. データベースマイグレーション実行
2. Program Director初期化
3. WebSocket配信開始
4. Host Agentとの連携設定

## 今後の拡張予定

- 複数チャンネル対応
- SIP統合（電話回線）
- 詳細分析機能
- 録音・クリップ生成機能
