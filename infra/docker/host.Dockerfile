FROM golang:1.23-alpine AS builder

WORKDIR /app

# 依存関係をコピー
COPY services/host/go.mod services/host/go.sum ./
RUN go mod download

# ソースコードをコピー
COPY services/host/ ./
COPY pkg ./pkg

# ビルド
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

# 実行用イメージ
FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /root/

# ビルドしたバイナリをコピー
COPY --from=builder /app/main .

# 実行
CMD ["./main"]
