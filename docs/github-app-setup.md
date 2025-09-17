# Cloud Build GitHub Application セットアップガイド

## 概要

このガイドでは、Cloud Build GitHub Applicationを設定して、GitHubリポジトリからCloud Buildをトリガーする方法を説明します。

## 設定手順

### 1. GitHub Applicationの有効化

1. [Google Cloud Console](https://console.cloud.google.com/)にアクセス
2. プロジェクトを選択
3. ナビゲーションメニュー > CI/CD > Cloud Build
4. 「GitHub アプリ」タブを選択
5. 「GitHub アプリを有効にする」をクリック

### 2. リポジトリの接続

1. 「リポジトリを接続」をクリック
2. GitHubアカウントで認証
3. 接続したいリポジトリを選択
4. 「接続」をクリック

### 3. ビルドトリガーの作成

1. 「トリガーを作成」をクリック
2. 以下の設定を行う：
   - **名前**: `radio24-deploy`
   - **イベント**: `Push to a branch`
   - **ブランチ**: `^main$`
   - **設定**: `Cloud Build 設定ファイル (yaml または json)`
   - **場所**: `/cloudbuild/cloudbuild.yaml`

### 4. Secret Managerの設定確認

以下のシークレットが正しく設定されていることを確認：

```bash
gcloud secrets list
```

必要なシークレット：

- `postgres-password`
- `openai-api-key`
- `livekit-api-key`
- `livekit-api-secret`
- `livekit-url`

### 5. 権限の設定

Cloud Buildサービスアカウントに以下の権限が必要：

```bash
# Secret Manager アクセサー
gcloud projects add-iam-policy-binding $PROJECT_ID \
    --member="serviceAccount:$PROJECT_NUMBER@cloudbuild.gserviceaccount.com" \
    --role="roles/secretmanager.secretAccessor"

# Cloud Run 管理者
gcloud projects add-iam-policy-binding $PROJECT_ID \
    --member="serviceAccount:$PROJECT_NUMBER@cloudbuild.gserviceaccount.com" \
    --role="roles/run.admin"

# Container Registry 書き込み
gcloud projects add-iam-policy-binding $PROJECT_ID \
    --member="serviceAccount:$PROJECT_NUMBER@cloudbuild.gserviceaccount.com" \
    --role="roles/storage.admin"
```

## 使用方法

### 自動デプロイ

- `main`ブランチへのプッシュ時に自動的にデプロイが実行されます
- プルリクエストのマージ時にデプロイがトリガーされます

### 手動デプロイ

GitHub上で「Actions」タブから手動でビルドをトリガーできます。

### ビルドログの確認

- Google Cloud Console > Cloud Build でビルドログを確認
- GitHub上でも基本的なビルドステータスを確認可能

## トラブルシューティング

### よくある問題

1. **権限エラー**
   - サービスアカウントの権限を確認
   - IAMポリシーの設定を確認

2. **シークレットアクセスエラー**
   - Secret Managerの権限を確認
   - シークレット名のスペルチェック

3. **ネットワークエラー**
   - VPCコネクタの設定を確認
   - プライベートIPの設定を確認

## メリット

1. **セキュリティ**: Secret Managerとの統合により、機密情報を安全に管理
2. **統合性**: GCPサービスとの直接的な統合
3. **パフォーマンス**: GCP内での高速ビルド
4. **スケーラビリティ**: Cloud Buildの自動スケーリング
