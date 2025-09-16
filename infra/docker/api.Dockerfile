FROM golang:1.23 AS build
WORKDIR /app
COPY services/api ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y curl && rm -rf /var/lib/apt/lists/*
ENV PORT=8080
EXPOSE 8080
COPY --from=build /app/server /server
CMD ["/server"]
