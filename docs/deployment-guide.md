# デプロイメントガイド

## 概要

このガイドでは、24時間AIラジオシステムのGCPへのデプロイメント手順を説明します。

## 前提条件

### 必要なツール

- Google Cloud SDK (gcloud)
- Docker
- Git
- GitHub アカウント

### GCPプロジェクトの準備

1. GCPコンソールでプロジェクトを作成
2. 必要なAPIを有効化
3. サービスアカウントの作成と権限設定

## 初期セットアップ

### 1. GCPプロジェクトの設定

```bash
# プロジェクトIDを設定
export PROJECT_ID="radio24-project"
export REGION="asia-northeast1"

# プロジェクトを設定
gcloud config set project $PROJECT_ID

# 必要なAPIを有効化
gcloud services enable cloudbuild.googleapis.com
gcloud services enable run.googleapis.com
gcloud services enable sqladmin.googleapis.com
gcloud services enable container.googleapis.com
gcloud services enable redis.googleapis.com
```

### 2. GitHub Secretsの設定

GitHubリポジトリのSettings > Secrets and variables > Actionsで以下のシークレットを設定：

```
POSTGRES_ROOT_PASSWORD: <root password>
POSTGRES_PASSWORD: <user password>
POSTGRES_HOST: <cloud sql instance ip>
POSTGRES_USER: radio24-user
POSTGRES_DB: radio24
OPENAI_API_KEY: <openai api key>
LIVEKIT_API_KEY: <livekit api key>
LIVEKIT_API_SECRET: <livekit api secret>
LIVEKIT_WS_URL: <livekit websocket url>
API_BASE_URL: <api service url>
```

## デプロイメント手順

### 1. インフラストラクチャのデプロイ

```bash
# インフラストラクチャワークフローを手動実行
gh workflow run infrastructure.yml
```

または、以下のコマンドで手動実行：

```bash
# Cloud SQLインスタンスの作成
gcloud sql instances create radio24-db \
  --database-version=POSTGRES_15 \
  --tier=db-f1-micro \
  --region=$REGION \
  --storage-type=SSD \
  --storage-size=10GB \
  --storage-auto-increase \
  --backup \
  --enable-ip-alias \
  --network=default \
  --no-assign-ip \
  --root-password=$POSTGRES_ROOT_PASSWORD

# データベースの作成
gcloud sql databases create radio24 --instance=radio24-db

# データベースユーザーの作成
gcloud sql users create radio24-user \
  --instance=radio24-db \
  --password=$POSTGRES_PASSWORD

# Redisインスタンスの作成
gcloud redis instances create radio24-redis \
  --size=1 \
  --region=$REGION \
  --redis-version=redis_7_0 \
  --tier=basic

# VPCコネクタの作成
gcloud compute networks vpc-access connectors create radio24-connector \
  --region=$REGION \
  --subnet=default \
  --subnet-project=$PROJECT_ID \
  --min-instances=2 \
  --max-instances=3
```

### 2. データベースマイグレーション

```bash
# Cloud SQL Auth Proxyの設定
wget https://dl.google.com/cloudsql/cloud_sql_proxy.linux.amd64 -O cloud_sql_proxy
chmod +x cloud_sql_proxy

# プロキシの起動
./cloud_sql_proxy -instances=$PROJECT_ID:$REGION:radio24-db=tcp:5432 &

# マイグレーションの実行
PGPASSWORD=$POSTGRES_PASSWORD psql -h localhost -U radio24-user -d radio24 -f db/init/001_init.sql
PGPASSWORD=$POSTGRES_PASSWORD psql -h localhost -U radio24-user -d radio24 -f db/migrations/002_schema.sql
```

### 3. アプリケーションのデプロイ

```bash
# メインのCI/CDワークフローを実行
gh workflow run ci.yml
```

または、手動でデプロイ：

```bash
# Docker認証の設定
gcloud auth configure-docker

# APIサービスのビルド・デプロイ
docker build -f infra/docker/api.Dockerfile -t gcr.io/$PROJECT_ID/api:latest .
docker push gcr.io/$PROJECT_ID/api:latest

gcloud run deploy api \
  --image gcr.io/$PROJECT_ID/api:latest \
  --platform managed \
  --region $REGION \
  --allow-unauthenticated \
  --port 8080 \
  --memory 1Gi \
  --cpu 1 \
  --max-instances 10 \
  --set-env-vars "POSTGRES_HOST=$POSTGRES_HOST,POSTGRES_PORT=5432,POSTGRES_USER=$POSTGRES_USER,POSTGRES_PASSWORD=$POSTGRES_PASSWORD,POSTGRES_DB=$POSTGRES_DB,OPENAI_API_KEY=$OPENAI_API_KEY,LIVEKIT_API_KEY=$LIVEKIT_API_KEY,LIVEKIT_API_SECRET=$LIVEKIT_API_SECRET"

# Webサービスのビルド・デプロイ
docker build -f infra/docker/web.Dockerfile -t gcr.io/$PROJECT_ID/web:latest .
docker push gcr.io/$PROJECT_ID/web:latest

gcloud run deploy web \
  --image gcr.io/$PROJECT_ID/web:latest \
  --platform managed \
  --region $REGION \
  --allow-unauthenticated \
  --port 3000 \
  --memory 512Mi \
  --cpu 1 \
  --max-instances 5 \
  --set-env-vars "NEXT_PUBLIC_API_BASE=$API_BASE_URL,NEXT_PUBLIC_OPENAI_REALTIME_MODEL=gpt-realtime"

# Hostサービスのビルド・デプロイ
docker build -f infra/docker/host.Dockerfile -t gcr.io/$PROJECT_ID/host:latest .
docker push gcr.io/$PROJECT_ID/host:latest

gcloud run deploy host \
  --image gcr.io/$PROJECT_ID/host:latest \
  --platform managed \
  --region $REGION \
  --no-allow-unauthenticated \
  --port 8080 \
  --memory 1Gi \
  --cpu 1 \
  --max-instances 1 \
  --set-env-vars "LIVEKIT_API_KEY=$LIVEKIT_API_KEY,LIVEKIT_API_SECRET=$LIVEKIT_API_SECRET,OPENAI_API_KEY=$OPENAI_API_KEY,LIVEKIT_WS_URL=$LIVEKIT_WS_URL"
```

### 4. LiveKitサービスのデプロイ

```bash
# LiveKitワークフローを実行
gh workflow run livekit.yml
```

または、手動でデプロイ：

```bash
# LiveKitイメージのビルド・デプロイ
docker build -f infra/livekit/Dockerfile -t gcr.io/$PROJECT_ID/livekit:latest infra/livekit/
docker push gcr.io/$PROJECT_ID/livekit:latest

gcloud run deploy livekit \
  --image gcr.io/$PROJECT_ID/livekit:latest \
  --platform managed \
  --region $REGION \
  --allow-unauthenticated \
  --port 7880 \
  --memory 2Gi \
  --cpu 2 \
  --max-instances 3 \
  --set-env-vars "LIVEKIT_KEYS=$LIVEKIT_API_KEY:$LIVEKIT_API_SECRET"
```

## デプロイメント後の確認

### 1. サービスの状態確認

```bash
# 全サービスの状態確認
gcloud run services list --region=$REGION

# 各サービスの詳細確認
gcloud run services describe api --region=$REGION
gcloud run services describe web --region=$REGION
gcloud run services describe host --region=$REGION
gcloud run services describe livekit --region=$REGION
```

### 2. ヘルスチェック

```bash
# APIサービスのヘルスチェック
curl -f https://api-<hash>-uc.a.run.app/health

# Webサービスのヘルスチェック
curl -f https://web-<hash>-uc.a.run.app

# LiveKitサービスのヘルスチェック
curl -f https://livekit-<hash>-uc.a.run.app
```

### 3. ログの確認

```bash
# 各サービスのログ確認
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=api" --limit=50
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=web" --limit=50
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=host" --limit=50
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=livekit" --limit=50
```

## トラブルシューティング

### よくある問題と解決方法

#### 1. デプロイメントの失敗

**問題**: サービスがデプロイできない
**解決方法**:

- ログを確認してエラーの詳細を把握
- 環境変数の設定を確認
- リソース制限を確認
- 権限設定を確認

#### 2. データベース接続エラー

**問題**: アプリケーションがデータベースに接続できない
**解決方法**:

- VPCコネクタの設定を確認
- データベースのIPアドレスを確認
- ファイアウォールルールを確認
- 認証情報を確認

#### 3. LiveKit接続エラー

**問題**: LiveKitサービスに接続できない
**解決方法**:

- WebSocket URLの設定を確認
- APIキーとシークレットを確認
- ネットワーク設定を確認
- ポート設定を確認

#### 4. OpenAI API接続エラー

**問題**: OpenAI APIに接続できない
**解決方法**:

- APIキーの有効性を確認
- レート制限を確認
- ネットワーク接続を確認
- プロキシ設定を確認

## 監視の設定

### 1. Cloud Monitoringの設定

```bash
# アラートポリシーの作成
gcloud alpha monitoring policies create --policy-from-file=monitoring/error-rate-policy.yaml
gcloud alpha monitoring policies create --policy-from-file=monitoring/latency-policy.yaml
gcloud alpha monitoring policies create --policy-from-file=monitoring/resource-usage-policy.yaml
```

### 2. ログベースメトリクスの設定

```bash
# エラー率メトリクスの作成
gcloud logging metrics create error_rate \
  --description="Error rate metric" \
  --log-filter="resource.type=cloud_run_revision AND severity>=ERROR"
```

## バックアップの設定

### 1. データベースバックアップ

```bash
# 日次バックアップの設定
gcloud sql backups create --instance=radio24-db --description="Daily backup"
```

### 2. ストレージバックアップ

```bash
# Cloud Storageバケットの作成
gsutil mb gs://radio24-backups

# バックアップスクリプトの設定
gcloud scheduler jobs create http backup-job \
  --schedule="0 2 * * *" \
  --uri="https://api-<hash>-uc.a.run.app/api/admin/backup" \
  --http-method=POST
```

## セキュリティの設定

### 1. IAMロールの設定

```bash
# サービスアカウントの作成
gcloud iam service-accounts create radio24-deployer \
  --display-name="Radio24 Deployer"

# 必要な権限の付与
gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/run.admin"

gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/storage.admin"
```

### 2. シークレット管理

```bash
# Cloud Secret Managerの有効化
gcloud services enable secretmanager.googleapis.com

# シークレットの作成
echo -n "$POSTGRES_PASSWORD" | gcloud secrets create postgres-password --data-file=-
echo -n "$OPENAI_API_KEY" | gcloud secrets create openai-api-key --data-file=-
echo -n "$LIVEKIT_API_SECRET" | gcloud secrets create livekit-api-secret --data-file=-
```

## パフォーマンス最適化

### 1. リソースの調整

```bash
# APIサービスのリソース調整
gcloud run services update api \
  --region=$REGION \
  --memory=2Gi \
  --cpu=2 \
  --max-instances=20

# Webサービスのリソース調整
gcloud run services update web \
  --region=$REGION \
  --memory=1Gi \
  --cpu=1 \
  --max-instances=10
```

### 2. キャッシュの設定

```bash
# Redisインスタンスの拡張
gcloud redis instances update radio24-redis \
  --size=2 \
  --region=$REGION
```

## ロールバック手順

### 1. 前バージョンへの復旧

```bash
# 特定のリビジョンへのロールバック
gcloud run services update-traffic api \
  --region=$REGION \
  --to-revisions=api-<previous-revision>=100

# または、特定のコミットSHAへのロールバック
gh workflow run rollback.yml --ref=<commit-sha>
```

### 2. 緊急時の対応

```bash
# 全サービスの停止
gcloud run services update api --region=$REGION --min-instances=0
gcloud run services update web --region=$REGION --min-instances=0
gcloud run services update host --region=$REGION --min-instances=0
gcloud run services update livekit --region=$REGION --min-instances=0
```

## メンテナンス

### 1. 定期メンテナンス

```bash
# 依存関係の更新
gh workflow run dependency-update.yml

# セキュリティスキャン
gh workflow run security-scan.yml

# パフォーマンステスト
gh workflow run performance-test.yml
```

### 2. クリーンアップ

```bash
# 古いリビジョンの削除
gcloud run revisions delete <old-revision> --region=$REGION

# 古いイメージの削除
gcloud container images delete gcr.io/$PROJECT_ID/api:old-tag
gcloud container images delete gcr.io/$PROJECT_ID/web:old-tag
gcloud container images delete gcr.io/$PROJECT_ID/host:old-tag
gcloud container images delete gcr.io/$PROJECT_ID/livekit:old-tag
```

## まとめ

このガイドに従って、24時間AIラジオシステムをGCPにデプロイできます。デプロイメント後は、監視とメンテナンスを継続的に行い、システムの安定性とパフォーマンスを維持してください。

問題が発生した場合は、ログを確認し、適切なトラブルシューティング手順に従って解決してください。
