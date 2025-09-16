FROM golang:1.23-alpine AS builder

WORKDIR /app

# 依存関係をコピー
COPY services/host/go.mod services/host/go.sum ./
RUN go mod download

# ソースコードをコピー
COPY services/host/ ./

# ビルド
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o host .

# 実行用イメージ
FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /app

# ビルドしたバイナリをコピー
COPY --from=builder /app/host .

# .envファイル用のディレクトリを作成（ボリュームマウント用）
RUN mkdir -p /app

# 実行
CMD ["./host"]
