# 24時間AIラジオシステム

## 概要

24時間連続でAI DJが放送を行うラジオシステムです。OpenAI Realtime APIとLiveKitを使用してリアルタイム音声配信を実現します。

## アーキテクチャ

- **フロントエンド**: Next.js (React)
- **バックエンド**: Go (API + Host)
- **リアルタイム通信**: LiveKit (WebRTC SFU)
- **データベース**: PostgreSQL + pgvector
- **キャッシュ**: Redis
- **インフラ**: Google Cloud Platform
- **CI/CD**: GitHub Actions + Cloud Build

## セットアップ

### 前提条件

- Google Cloud Platform アカウント
- GitHub アカウント
- Docker
- Terraform
- gcloud CLI

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
gcloud services enable compute.googleapis.com
gcloud services enable vpcaccess.googleapis.com
gcloud services enable secretmanager.googleapis.com
```

### 2. サービスアカウントの作成

```bash
# サービスアカウントの作成
gcloud iam service-accounts create radio24-deployer \
  --display-name="Radio24 Deployer" \
  --description="Service account for Radio24 deployment"

# 必要な権限の付与
gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/run.admin"

gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/storage.admin"

gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/cloudsql.admin"

gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/redis.admin"

gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/monitoring.admin"

gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/secretmanager.admin"

gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/compute.admin"

# サービスアカウントキーの作成
gcloud iam service-accounts keys create radio24-deployer-key.json \
  --iam-account=radio24-deployer@$PROJECT_ID.iam.gserviceaccount.com
```

### 3. GitHub Secretsの設定

GitHubリポジトリの **Settings > Secrets and variables > Actions** で以下のシークレットを設定：

```
GCP_SA_KEY: <サービスアカウントキーのJSON内容>
POSTGRES_PASSWORD: <データベースユーザーパスワード>
OPENAI_API_KEY: <OpenAI APIキー>
LIVEKIT_API_KEY: <LiveKit APIキー>
LIVEKIT_API_SECRET: <LiveKit APIシークレット>
```

### 4. Terraform変数の設定

```bash
# terraform.tfvarsファイルを作成
cp terraform/terraform.tfvars.example terraform/terraform.tfvars

# 必要な変数を設定
cat > terraform/terraform.tfvars << EOF
project_id = "$PROJECT_ID"
region     = "$REGION"
postgres_password = "your-secure-password"
openai_api_key    = "your-openai-api-key"
livekit_api_key   = "your-livekit-api-key"
livekit_api_secret = "your-livekit-api-secret"
EOF
```

### 5. インフラストラクチャのデプロイ

#### Docker Composeを使用する場合

```bash
# 環境変数ファイルを作成
cp .env.example .env
# .envファイルに必要な値を設定

# GCPサービスアカウントキーを配置
cp /path/to/your/service-account-key.json gcp-key.json

# Terraformでインフラをデプロイ
make tf-init    # Terraformを初期化
make tf-plan    # プランを確認
make tf-apply   # インフラをデプロイ
```

#### スクリプトを使用する場合

```bash
# Terraformスクリプトを使用
./scripts/terraform.sh init
./scripts/terraform.sh plan
./scripts/terraform.sh apply
```

#### 直接Terraformを使用する場合

```bash
# Terraformでインフラをデプロイ
cd terraform
terraform init
terraform plan
terraform apply
```

### 6. アプリケーションのデプロイ

```bash
# Cloud Buildでアプリケーションをデプロイ
make cb-deploy
# または
gcloud builds submit --config cloudbuild/cloudbuild.yaml
```

## 開発

### ローカル開発環境

```bash
# Docker Composeでローカル環境を起動
docker-compose up -d

# サービスにアクセス
# Web: http://localhost:3000
# API: http://localhost:8080
# LiveKit: http://localhost:7880
```

### Terraform操作

```bash
# Makefileを使用
make tf-init      # Terraformを初期化
make tf-plan      # プランを確認
make tf-apply     # インフラをデプロイ
make tf-destroy   # リソースを削除
make tf-output    # 出力を表示
make tf-validate  # 設定を検証
make tf-fmt       # ファイルをフォーマット

# スクリプトを使用
./scripts/terraform.sh init
./scripts/terraform.sh plan
./scripts/terraform.sh apply

# Docker Composeを直接使用
docker-compose --profile terraform run --rm terraform terraform plan
docker-compose --profile terraform run --rm terraform terraform apply
```

### テスト

```bash
# Goテスト
cd services/api && go test ./...
cd services/host && go test ./...

# フロントエンドテスト
cd apps/web && pnpm test

# 統合テスト
gcloud builds submit --config cloudbuild/cloudbuild-test.yaml
```

## デプロイメント

### 自動デプロイ

mainブランチへのプッシュで自動的にデプロイされます：

1. **テスト**: GitHub Actionsでテストを実行
2. **ビルド**: Cloud BuildでDockerイメージをビルド
3. **デプロイ**: Terraformでインフラを更新、Cloud Runにデプロイ

### 手動デプロイ

```bash
# Cloud Buildでデプロイ
gcloud builds submit --config cloudbuild/cloudbuild.yaml \
  --substitutions _COMMIT_SHA=$(git rev-parse HEAD)
```

## 監視

### サービスURL

デプロイ後、以下のURLでサービスにアクセスできます：

- **API**: `https://api-<hash>-uc.a.run.app`
- **Web**: `https://web-<hash>-uc.a.run.app`
- **LiveKit**: `https://livekit-<hash>-uc.a.run.app`

### ヘルスチェック

```bash
# APIヘルスチェック
curl https://api-<hash>-uc.a.run.app/health

# Webヘルスチェック
curl https://web-<hash>-uc.a.run.app

# LiveKitヘルスチェック
curl https://livekit-<hash>-uc.a.run.app
```

## トラブルシューティング

### よくある問題

1. **認証エラー**: サービスアカウントキーと権限を確認
2. **デプロイ失敗**: Cloud Buildログを確認
3. **データベース接続エラー**: VPCコネクタの設定を確認

### ログの確認

```bash
# Cloud Runログ
gcloud logging read "resource.type=cloud_run_revision" --limit=100

# Cloud Buildログ
gcloud builds log <build-id>
```

## ライセンス

MIT License
