FROM livekit/livekit-server:latest

# LiveKit設定ファイルをコピー
COPY livekit.yaml /etc/livekit.yaml

# ポートを公開
EXPOSE 7880 7881

# LiveKitサーバーを起動
CMD ["--config", "/etc/livekit.yaml"]
