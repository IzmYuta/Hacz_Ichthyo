FROM golang:1.24-alpine AS builder

# CGOとオーディオ処理に必要なパッケージをインストール
RUN apk add --no-cache \
    pkgconfig \
    opus-dev \
    opusfile-dev \
    soxr-dev \
    gcc \
    musl-dev

WORKDIR /app

# 依存関係をコピー
COPY services/host/go.mod services/host/go.sum ./
RUN go mod download

# ソースコードをコピー
COPY services/host/ ./

# ビルド（CGOを有効にする）
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o host .

# 実行用イメージ
FROM alpine:latest

RUN apk --no-cache add ca-certificates opus opusfile soxr
WORKDIR /app

# ビルドしたバイナリをコピー
COPY --from=builder /app/host .

# .envファイル用のディレクトリを作成（ボリュームマウント用）
RUN mkdir -p /app

# 実行
CMD ["./host"]
