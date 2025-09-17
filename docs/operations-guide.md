# 運用ガイド

## 概要

このガイドでは、24時間AIラジオシステムの日常的な運用、監視、メンテナンスについて説明します。

## 日常運用

### 1. システム監視

#### ヘルスチェック

```bash
# 各サービスのヘルスチェック
curl -f https://api-<hash>-uc.a.run.app/health
curl -f https://web-<hash>-uc.a.run.app
curl -f https://livekit-<hash>-uc.a.run.app

# データベース接続確認
gcloud sql instances describe radio24-db --region=asia-northeast1

# Redis接続確認
gcloud redis instances describe radio24-redis --region=asia-northeast1
```

#### メトリクス確認

```bash
# Cloud Runサービスのメトリクス
gcloud run services describe api --region=asia-northeast1
gcloud run services describe web --region=asia-northeast1
gcloud run services describe host --region=asia-northeast1
gcloud run services describe livekit --region=asia-northeast1

# リソース使用率確認
gcloud monitoring metrics list --filter="resource.type=cloud_run_revision"
```

### 2. ログ監視

#### リアルタイムログ確認

```bash
# APIサービスのログ
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=api" --limit=100 --format="table(timestamp,severity,textPayload)"

# Hostサービスのログ（台本生成・TTS）
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=host" --limit=100 --format="table(timestamp,severity,textPayload)"

# Hostサービスの台本生成ログ
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=host AND textPayload:\"Generating script\"" --limit=50 --format="table(timestamp,textPayload)"

# HostサービスのTTS生成ログ
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=host AND textPayload:\"TTS\"" --limit=50 --format="table(timestamp,textPayload)"

# LiveKitサービスのログ
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=livekit" --limit=100 --format="table(timestamp,severity,textPayload)"

# エラーログの確認
gcloud logging read "severity>=ERROR" --limit=50 --format="table(timestamp,resource.labels.service_name,textPayload)"
```

#### ログ分析

```bash
# エラー率の確認
gcloud logging read "severity>=ERROR" --limit=1000 | grep -c "ERROR"

# レスポンス時間の確認
gcloud logging read "resource.type=cloud_run_revision AND textPayload:\"response_time\"" --limit=100

# ユーザーアクティビティの確認
gcloud logging read "resource.type=cloud_run_revision AND textPayload:\"user_action\"" --limit=100

# Hostサービスの台本生成頻度確認
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=host AND textPayload:\"Generated script\"" --limit=100 | grep -c "Generated script"

# HostサービスのTTS生成頻度確認
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=host AND textPayload:\"TTS audio generated\"" --limit=100 | grep -c "TTS audio generated"
```

### 3. パフォーマンス監視

#### レスポンス時間監視

```bash
# APIレスポンス時間
gcloud monitoring metrics list --filter="metric.type=run.googleapis.com/request_latency"

# データベース接続時間
gcloud monitoring metrics list --filter="metric.type=cloudsql.googleapis.com/database/up"

# Redis接続時間
gcloud monitoring metrics list --filter="metric.type=redis.googleapis.com/instance/up"
```

#### スループット監視

```bash
# リクエスト数
gcloud monitoring metrics list --filter="metric.type=run.googleapis.com/request_count"

# 同時接続数
gcloud monitoring metrics list --filter="metric.type=run.googleapis.com/container/instance_count"
```

## アラート対応

### 1. アラートの種類と対応

#### 高エラー率アラート

**条件**: エラー率 > 5%
**対応手順**:

1. ログを確認してエラーの原因を特定
2. 必要に応じてサービスを再起動
3. 根本原因を調査・修正

```bash
# エラーログの確認
gcloud logging read "severity>=ERROR" --limit=100 --format="table(timestamp,resource.labels.service_name,textPayload)"

# サービス再起動
gcloud run services update api --region=asia-northeast1 --min-instances=0
gcloud run services update api --region=asia-northeast1 --min-instances=1
```

#### 高レイテンシアラート

**条件**: レスポンス時間 > 2秒
**対応手順**:

1. リソース使用率を確認
2. データベース接続を確認
3. 必要に応じてスケーリング

```bash
# リソース使用率確認
gcloud run services describe api --region=asia-northeast1

# スケーリング
gcloud run services update api --region=asia-northeast1 --max-instances=20
```

#### リソース使用率アラート

**条件**: CPU使用率 > 80% または メモリ使用率 > 90%
**対応手順**:

1. リソース使用率の詳細確認
2. 必要に応じてリソースを増強
3. アプリケーションの最適化を検討

```bash
# リソース使用率確認
gcloud monitoring metrics list --filter="metric.type=run.googleapis.com/container/cpu/utilizations"

# リソース増強
gcloud run services update api --region=asia-northeast1 --memory=2Gi --cpu=2
```

### 2. 緊急時対応

#### サービス停止時の対応

```bash
# 全サービスの状態確認
gcloud run services list --region=asia-northeast1

# サービス再起動
gcloud run services update api --region=asia-northeast1 --min-instances=0
gcloud run services update api --region=asia-northeast1 --min-instances=1

# ロールバック
gcloud run services update-traffic api --region=asia-northeast1 --to-revisions=api-<previous-revision>=100
```

#### データベース接続エラー時の対応

```bash
# データベース状態確認
gcloud sql instances describe radio24-db --region=asia-northeast1

# 接続テスト
gcloud sql connect radio24-db --user=radio24-user --database=radio24

# 必要に応じて再起動
gcloud sql instances restart radio24-db
```

## メンテナンス

### 1. 定期メンテナンス

#### 週次メンテナンス

```bash
# 依存関係の更新確認
gh workflow run dependency-update.yml

# セキュリティスキャン
gh workflow run security-scan.yml

# パフォーマンステスト
gh workflow run performance-test.yml
```

#### 月次メンテナンス

```bash
# コスト最適化分析
gh workflow run cost-optimization.yml

# コンプライアンスチェック
gh workflow run compliance.yml

# 災害復旧テスト
gh workflow run disaster-recovery.yml
```

### 2. データベースメンテナンス

#### バックアップ確認

```bash
# バックアップ一覧
gcloud sql backups list --instance=radio24-db

# 最新バックアップの確認
gcloud sql backups describe <backup-id> --instance=radio24-db
```

#### パフォーマンス最適化

```bash
# データベース統計の確認
gcloud sql instances describe radio24-db --region=asia-northeast1

# 必要に応じてインスタンスサイズ変更
gcloud sql instances patch radio24-db --tier=db-g1-small
```

### 3. アプリケーションメンテナンス

#### ログローテーション

```bash
# 古いログの削除
gcloud logging logs delete <log-name> --quiet
```

#### リソース最適化

```bash
# 未使用リソースの確認
gcloud run services list --region=asia-northeast1

# 古いリビジョンの削除
gcloud run revisions delete <revision-name> --region=asia-northeast1
```

## セキュリティ運用

### 1. セキュリティ監視

#### 脆弱性スキャン

```bash
# 定期的なセキュリティスキャン
gh workflow run security-scan.yml

# 手動での脆弱性確認
gcloud container images scan gcr.io/radio24-project/api:latest
```

#### アクセス監査

```bash
# IAMポリシーの確認
gcloud projects get-iam-policy radio24-project

# アクセスログの確認
gcloud logging read "resource.type=cloud_run_revision AND textPayload:\"access\"" --limit=100
```

### 2. シークレット管理

#### シークレットの更新

```bash
# シークレットの更新
echo -n "new-password" | gcloud secrets versions add postgres-password --data-file=-

# シークレットの確認
gcloud secrets versions list postgres-password
```

#### アクセス権限の確認

```bash
# シークレットへのアクセス権限確認
gcloud secrets get-iam-policy postgres-password
```

## パフォーマンス最適化

### 1. リソース最適化

#### CPU使用率の最適化

```bash
# CPU使用率の確認
gcloud monitoring metrics list --filter="metric.type=run.googleapis.com/container/cpu/utilizations"

# 必要に応じてCPUを増強
gcloud run services update api --region=asia-northeast1 --cpu=2
```

#### メモリ使用率の最適化

```bash
# メモリ使用率の確認
gcloud monitoring metrics list --filter="metric.type=run.googleapis.com/container/memory/utilizations"

# 必要に応じてメモリを増強
gcloud run services update api --region=asia-northeast1 --memory=2Gi
```

### 2. データベース最適化

#### クエリ最適化

```bash
# データベース接続
gcloud sql connect radio24-db --user=radio24-user --database=radio24

# スロークエリの確認
SELECT query, mean_time, calls FROM pg_stat_statements ORDER BY mean_time DESC LIMIT 10;

# インデックスの確認
SELECT schemaname, tablename, indexname, indexdef FROM pg_indexes WHERE tablename = 'submission';
```

#### 接続プール最適化

```bash
# 接続数の確認
SELECT count(*) FROM pg_stat_activity;

# 最大接続数の確認
SHOW max_connections;
```

### 3. キャッシュ最適化

#### Redis最適化

```bash
# Redis接続確認
gcloud redis instances describe radio24-redis --region=asia-northeast1

# 必要に応じてサイズ変更
gcloud redis instances update radio24-redis --size=2 --region=asia-northeast1
```

## トラブルシューティング

### 1. よくある問題と解決方法

#### サービスが起動しない

**症状**: Cloud Runサービスが起動しない
**原因**: 環境変数の設定ミス、リソース不足
**解決方法**:

```bash
# ログを確認
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=api" --limit=50

# 環境変数を確認
gcloud run services describe api --region=asia-northeast1

# リソースを確認
gcloud run services describe api --region=asia-northeast1 --format="value(spec.template.spec.containers[0].resources)"
```

#### データベース接続エラー

**症状**: アプリケーションがデータベースに接続できない
**原因**: VPCコネクタの設定、ファイアウォールルール
**解決方法**:

```bash
# VPCコネクタの確認
gcloud compute networks vpc-access connectors describe radio24-connector --region=asia-northeast1

# ファイアウォールルールの確認
gcloud compute firewall-rules list --filter="name~radio24"

# データベース接続テスト
gcloud sql connect radio24-db --user=radio24-user --database=radio24
```

#### LiveKit接続エラー

**症状**: LiveKitサービスに接続できない
**原因**: WebSocket URLの設定、APIキーの設定
**解決方法**:

```bash
# LiveKitサービスの確認
gcloud run services describe livekit --region=asia-northeast1

# 環境変数の確認
gcloud run services describe livekit --region=asia-northeast1 --format="value(spec.template.spec.containers[0].env[].value)"

# 接続テスト
curl -f https://livekit-<hash>-uc.a.run.app
```

### 2. ログ分析

#### エラーパターンの分析

```bash
# エラーログの集計
gcloud logging read "severity>=ERROR" --limit=1000 | grep -o "ERROR.*" | sort | uniq -c | sort -nr

# 時間帯別エラー分析
gcloud logging read "severity>=ERROR" --limit=1000 | grep -o "ERROR.*" | cut -d' ' -f1-2 | sort | uniq -c
```

#### パフォーマンス分析

```bash
# レスポンス時間の分析
gcloud logging read "resource.type=cloud_run_revision AND textPayload:\"response_time\"" --limit=1000 | grep -o "response_time:[0-9]*" | cut -d: -f2 | sort -n

# リクエスト数の分析
gcloud logging read "resource.type=cloud_run_revision AND textPayload:\"request_count\"" --limit=1000 | grep -o "request_count:[0-9]*" | cut -d: -f2 | sort -n
```

## 運用自動化

### 1. 自動化スクリプト

#### ヘルスチェックスクリプト

```bash
#!/bin/bash
# health-check.sh

API_URL="https://api-<hash>-uc.a.run.app/health"
WEB_URL="https://web-<hash>-uc.a.run.app"
LIVEKIT_URL="https://livekit-<hash>-uc.a.run.app"

check_service() {
    local url=$1
    local name=$2
    
    if curl -f -s "$url" > /dev/null; then
        echo "✓ $name is healthy"
        return 0
    else
        echo "✗ $name is unhealthy"
        return 1
    fi
}

check_service "$API_URL" "API Service"
check_service "$WEB_URL" "Web Service"
check_service "$LIVEKIT_URL" "LiveKit Service"
```

#### ログ分析スクリプト

```bash
#!/bin/bash
# log-analysis.sh

echo "=== Error Analysis ==="
gcloud logging read "severity>=ERROR" --limit=100 --format="table(timestamp,resource.labels.service_name,textPayload)"

echo "=== Performance Analysis ==="
gcloud logging read "resource.type=cloud_run_revision AND textPayload:\"response_time\"" --limit=100 --format="table(timestamp,textPayload)"

echo "=== Resource Usage ==="
gcloud monitoring metrics list --filter="metric.type=run.googleapis.com/container/cpu/utilizations" --limit=10
```

### 2. 監視ダッシュボード

#### カスタムダッシュボードの作成

```bash
# ダッシュボードの作成
gcloud monitoring dashboards create --config-from-file=dashboard-config.json
```

#### アラートポリシーの設定

```bash
# アラートポリシーの作成
gcloud alpha monitoring policies create --policy-from-file=monitoring/error-rate-policy.yaml
gcloud alpha monitoring policies create --policy-from-file=monitoring/latency-policy.yaml
gcloud alpha monitoring policies create --policy-from-file=monitoring/resource-usage-policy.yaml
```

## 運用レポート

### 1. 日次レポート

#### システム状況レポート

```bash
#!/bin/bash
# daily-report.sh

echo "=== Daily System Report ==="
echo "Date: $(date)"
echo ""

echo "=== Service Status ==="
gcloud run services list --region=asia-northeast1 --format="table(metadata.name,status.url,status.conditions[0].status)"

echo "=== Resource Usage ==="
gcloud monitoring metrics list --filter="metric.type=run.googleapis.com/container/cpu/utilizations" --limit=10

echo "=== Error Count ==="
gcloud logging read "severity>=ERROR" --limit=1000 | grep -c "ERROR"

echo "=== Request Count ==="
gcloud logging read "resource.type=cloud_run_revision AND textPayload:\"request_count\"" --limit=1000 | grep -c "request_count"
```

### 2. 週次レポート

#### パフォーマンスレポート

```bash
#!/bin/bash
# weekly-report.sh

echo "=== Weekly Performance Report ==="
echo "Week: $(date)"
echo ""

echo "=== Average Response Time ==="
gcloud logging read "resource.type=cloud_run_revision AND textPayload:\"response_time\"" --limit=1000 | grep -o "response_time:[0-9]*" | cut -d: -f2 | awk '{sum+=$1; count++} END {print "Average:", sum/count "ms"}'

echo "=== Peak Request Rate ==="
gcloud logging read "resource.type=cloud_run_revision AND textPayload:\"request_count\"" --limit=1000 | grep -o "request_count:[0-9]*" | cut -d: -f2 | sort -n | tail -1

echo "=== Error Rate ==="
ERROR_COUNT=$(gcloud logging read "severity>=ERROR" --limit=1000 | grep -c "ERROR")
TOTAL_COUNT=$(gcloud logging read "resource.type=cloud_run_revision" --limit=1000 | grep -c "request_count")
echo "Error Rate: $((ERROR_COUNT * 100 / TOTAL_COUNT))%"
```

## まとめ

この運用ガイドに従って、24時間AIラジオシステムの日常的な運用、監視、メンテナンスを効率的に行うことができます。定期的な監視とメンテナンスにより、システムの安定性とパフォーマンスを維持してください。

問題が発生した場合は、適切なトラブルシューティング手順に従って迅速に対応し、必要に応じてエスカレーションしてください。
