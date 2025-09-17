# Director Service Dockerfile
FROM golang:1.23-alpine AS builder

WORKDIR /app

# 依存関係をコピーしてインストール
COPY services/director/go.mod services/director/go.sum ./
RUN go mod download

# ソースコードをコピー
COPY services/director/ ./

# バイナリをビルド
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o director .

# 実行用イメージ
FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /root/

# バイナリをコピー
COPY --from=builder /app/director .

# ポート8081を公開
EXPOSE 8081

# ヘルスチェック
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8081/health || exit 1

# アプリケーションを実行
CMD ["./director"]
