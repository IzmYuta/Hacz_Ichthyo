（テーマ：**24時間AIラジオ局「Radio‑24」**／スタック：**Next.js 15 + React 19、Go(API)、PostgreSQL(+pgvector)、Cloud Run**。音声は **OpenAI Realtime API (WebRTC)** を中核にします）

---

# Radio‑24 — Cursor実装ブリーフ（環境構築 & 実装手順）

## ゴール（Definition of Done）

* ブラウザで **「ON AIR」画面**を開くと、\*\*PTT（Push‑to‑Talk）\*\*で話しかけ → **AIが生声で即時返答**（WebRTC/Realtime）。
* **毎正時**に番組テーマ（表示色/テキスト）が自動で切替。
* 投稿テキストを保存し、**pgvector**で類似投稿を3件レコメンド。
* サーバ秘密鍵はクライアントへ露出せず、**ephemeralなクライアントシークレット**でRealtimeへ接続。([OpenAI][1])

---

## 0. 前提・バージョン

* Node.js **>= 20** / pnpm 推奨
* Go **>= 1.22**
* Docker / Docker Compose
* GCP: Cloud Run / Cloud SQL (PostgreSQL 16)
* OpenAI API契約（**Realtime GA** / **gpt‑realtime** ボイス **Marin/Cedar**を利用）([OpenAI][1])
* **React 19 + Next.js 15**（App Router）([Next.js][2])

---

## 1. リポジトリ構成（Cursorにそのまま作成させる）

```
radio-24/
├─ apps/
│  └─ web/             # Next.js 15 + React 19
├─ services/
│  └─ api/             # Go (Cloud Run) - Realtime用ephemeral発行/テーマ配信/DB書き込み
├─ db/
│  ├─ migrations/      # SQL (pgvector拡張/テーブル/インデックス)
│  └─ init/            # docker-compose用 初期化SQL
├─ infra/
│  ├─ docker/          # 各サービスのDockerfile
│  └─ cloudrun/        # デプロイ用設定 (env, gcloud コマンド例)
├─ docker-compose.yml  # local: Postgres(pgvector)
├─ .env.example
└─ docs/
   └─ cursor-setup-radio24.md  # ←このファイル
```

---

## 2. シークレット & .env（ローカル）

`.env.example` を作成：

```
# OpenAI
OPENAI_API_KEY=sk-xxx  # サーバ側のみ
OPENAI_REALTIME_MODEL=gpt-realtime
OPENAI_REALTIME_VOICE=marin

# DB (local dev)
POSTGRES_USER=postgres
POSTGRES_PASSWORD=postgres
POSTGRES_DB=radio24
POSTGRES_PORT=5432

# API
API_PORT=8080
ALLOWED_ORIGIN=http://localhost:3000
```

> **注意**：RealtimeのWebRTC接続では、**標準APIキーをクライアントに渡さない**。サーバで**ephemeral（短命）クライアントシークレット**を発行して渡すのが推奨です（TTLは**約1分**）。([OpenAI][1])

---

## 3. DB（PostgreSQL + pgvector）ローカル起動

`docker-compose.yml`：

```yaml
version: "3.9"
services:
  db:
    image: ankane/pgvector:latest
    container_name: radio24-db
    ports: ["5432:5432"]
    environment:
      POSTGRES_USER: ${POSTGRES_USER:-postgres}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-postgres}
      POSTGRES_DB: ${POSTGRES_DB:-radio24}
    volumes:
      - ./db/init:/docker-entrypoint-initdb.d
```

`db/init/001_init.sql`：

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

`db/migrations/002_schema.sql`：

```sql
-- 投稿保存 (簡略)
CREATE TABLE IF NOT EXISTS submission (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT,
  type TEXT CHECK (type IN ('text','audio')) NOT NULL,
  text TEXT,
  embed VECTOR(1536),            -- pgvector
  created_at TIMESTAMPTZ DEFAULT now()
);

-- 類似検索を速くするHNSWインデックス（0.5+）
CREATE INDEX IF NOT EXISTS submission_embed_hnsw
ON submission USING hnsw (embed vector_cosine_ops);
```

> `pgvector` は HNSW/IVFFlat 等の近似インデックスをサポート。上記は HNSW 例。([GitHub][3])

**起動**：

```sh
docker compose up -d
```

---

## 4. Go API（services/api）

### 4.1 役割

* `POST /v1/realtime/ephemeral`：**ephemeral クライアントシークレット発行**（OpenAIの **`/v1/realtime/client_secrets`** を叩く）
* `POST /v1/theme/rotate`：毎正時のテーマ切替（後でWS配信）
* `POST /v1/submission`：投稿保存＋埋め込み作成（pgvector格納）

> Realtime GAでは、**`/v1/realtime/client_secrets`** でクライアントシークレットを発行し、WebRTC/SIP での接続に用います。([OpenAI][1])

### 4.2 依存

```sh
cd services/api
go mod init github.com/yourname/radio-24/api
go get github.com/go-chi/chi/v5
go get github.com/joho/godotenv
go get github.com/jackc/pgx/v5/stdlib
```

### 4.3 ハンドラ骨子（Cursorに作成させる）

`main.go`（ダイジェスト）：

```go
package main

import (
  "bytes"
  "database/sql"
  "encoding/json"
  "io"
  "log"
  "net/http"
  "os"
  "time"

  "github.com/go-chi/chi/v5"
  _ "github.com/jackc/pgx/v5/stdlib"
)

type EphemeralResp struct {
  ClientSecret struct {
    Value     string `json:"value"`
    ExpiresAt int64  `json:"expires_at"`
  } `json:"client_secret"`
}

// 環境変数読み込み & DB接続は省略（Cursorに補完させる）

func main() {
  r := chi.NewRouter()
  r.Use(cors())

  r.Post("/v1/realtime/ephemeral", handleEphemeral)
  r.Post("/v1/submission", handleSubmission)
  r.Post("/v1/theme/rotate", handleThemeRotate)

  http.ListenAndServe(":"+getEnv("API_PORT","8080"), r)
}

func handleEphemeral(w http.ResponseWriter, r *http.Request) {
  // OpenAIの client_secrets を叩いて短命キーを発行
  payload := map[string]any{
    "session": map[string]any{
      "type": "realtime",
      // ここでMCPやturn_detection等の既定を与えることも可能
    },
  }
  b, _ := json.Marshal(payload)

  req, _ := http.NewRequest("POST", "https://api.openai.com/v1/realtime/client_secrets", bytes.NewReader(b))
  req.Header.Set("Authorization", "Bearer "+os.Getenv("OPENAI_API_KEY"))
  req.Header.Set("Content-Type", "application/json")

  res, err := http.DefaultClient.Do(req)
  if err != nil { http.Error(w, err.Error(), 500); return }
  defer res.Body.Close()
  body, _ := io.ReadAll(res.Body)

  // 受け取った value のみをフロントへ返す（最小化）
  var parsed EphemeralResp
  if err := json.Unmarshal(body, &parsed); err != nil || parsed.ClientSecret.Value == "" {
    // ドキュメント更新等でshapeが変わる可能性に備え原文も返す
    w.Header().Set("Content-Type","application/json")
    w.Write(body); return
  }
  json.NewEncoder(w).Encode(map[string]any{
    "client_secret": parsed.ClientSecret.Value,
    "expires_at":    parsed.ClientSecret.ExpiresAt,
  })
}
```

> **補足**：Realtimeの**GAインターフェース**では設定項目（例：`session.update`、`server_vad`など）の扱いが**βから変更**されています。イベント名やパラメータは**GA版**に合わせてください。([OpenAI Developers][4])

`Dockerfile`（`infra/docker/api.Dockerfile`）：

```dockerfile
FROM golang:1.22 AS build
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server ./services/api

FROM gcr.io/distroless/base-debian12
ENV PORT=8080
EXPOSE 8080
COPY --from=build /app/server /server
CMD ["/server"]
```

---

## 5. Next.js（apps/web）

### 5.1 初期化

```sh
cd apps
pnpm create next-app web --ts --eslint --app --no-src-dir --use-pnpm
cd web
pnpm i
pnpm i @chakra-ui/react @emotion/react @emotion/styled framer-motion
```

`app/providers.tsx`（Chakra Provider）と `app/layout.tsx` を用意。

### 5.2 ON AIR ページ骨子（WebRTC接続）

`app/page.tsx`（抜粋・Cursorに最終化させる）：

```tsx
'use client';

import { useEffect, useRef, useState } from 'react';

export default function OnAir() {
  const [connected, setConnected] = useState(false);
  const [subtitles, setSubtitles] = useState('');
  const pcRef = useRef<RTCPeerConnection|null>(null);
  const dataRef = useRef<RTCDataChannel|null>(null);
  const remoteAudioRef = useRef<HTMLAudioElement>(null);

  async function connect() {
    // 1) サーバから短命クライアントシークレットを取得
    const eph = await fetch(process.env.NEXT_PUBLIC_API_BASE + '/v1/realtime/ephemeral', { method:'POST' }).then(r=>r.json());
    const token = eph.client_secret || eph.value; // shape差異に対応

    // 2) WebRTCピア接続
    const pc = new RTCPeerConnection();
    pcRef.current = pc;

    // 受信音声を再生
    pc.ontrack = (e) => {
      if (remoteAudioRef.current) {
        remoteAudioRef.current.srcObject = e.streams[0];
        remoteAudioRef.current.play().catch(()=>{});
      }
    };

    // イベント受信用データチャネル
    const dc = pc.createDataChannel('oai-events');
    dataRef.current = dc;
    dc.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        // 例: 字幕用（response.output_text.delta）
        if (msg.type === 'response.output_text.delta') {
          setSubtitles((s) => s + msg.delta);
        }
      } catch {}
    };

    // マイクを送信
    const ms = await navigator.mediaDevices.getUserMedia({ audio: true });
    for (const track of ms.getTracks()) pc.addTrack(track, ms);

    // SDPオファ生成
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);

    // 3) RealtimeへSDPをPOST（Bearer: ephemeral）
    const sdpResp = await fetch('https://api.openai.com/v1/realtime?model=' + (process.env.NEXT_PUBLIC_OPENAI_REALTIME_MODEL || 'gpt-realtime'), {
      method: 'POST',
      body: offer.sdp!,
      headers: {
        'Authorization': `Bearer ${token}`,
        'Content-Type': 'application/sdp'
      }
    });

    const answer = { type: 'answer', sdp: await sdpResp.text() } as RTCSessionDescriptionInit;
    await pc.setRemoteDescription(answer);

    // 4) セッション初期化（声・VAD・トーンなど）
    const init = {
      type: 'session.update',
      session: {
        type: 'realtime',
        instructions: 'あなたは深夜ラジオのDJ。短く・テンポ良く・ポジティブに。固有名詞ははっきり復唱。',
        voice: 'marin',
        audio: { input: { turn_detection: { type: 'server_vad', idle_timeout_ms: 6000 } } }
      }
    };
    dc.send(JSON.stringify(init));

    // 最初の挨拶
    dc.send(JSON.stringify({
      type: 'response.create',
      response: { modalities: ['audio','text'], instructions: 'ハックツラジオ、Radio-24へようこそ。30秒だけ投稿どうぞ！' }
    }));

    setConnected(true);
  }

  function disconnect() {
    dataRef.current?.close();
    pcRef.current?.close();
    setConnected(false);
  }

  return (
    <main className="p-6">
      <h1>Radio‑24 — ON AIR</h1>
      <div className="flex gap-4 items-center">
        {!connected ? <button onClick={connect}>接続</button> : <button onClick={disconnect}>切断</button>}
        <button onMouseDown={startPTT} onMouseUp={stopPTT} disabled={!connected}>🎙️ PTT</button>
      </div>
      <p style={{whiteSpace:'pre-wrap'}}>{subtitles}</p>
      <audio ref={remoteAudioRef} autoPlay />
    </main>
  );

  function startPTT(){ /* 任意：input_audio_buffer.append を使う場合の実装。MVPはserver_vadに任せてOK */ }
  function stopPTT(){ /* 任意 */ }
}
```

> WebRTCのSDP交換パターン（`offer`を `application/sdp` でPOST → `answer`を適用）は公式ブログのサンプルに準拠。([OpenAI][5])
> **GA以降**は\*\*`client_secrets`**と**`session.update`\*\*の扱いが刷新されています（温度パラメータ等の変更点に注意）。([OpenAI Developers][4])

**ポイント**

* **データチャネル `oai-events`** で `session.update` / `response.create` / `response.output_text.delta` 等のイベントをやり取りします。ガイダンスはWebRTCコミュニティ記事も参照。([webrtcHacks][6])
* **ボイスは `marin` or `cedar`** が推奨（Realtime専用の新ボイス）。([OpenAI][1])

---

## 6. 投稿保存 & 類似レコメンド（pgvector）

* `POST /v1/submission` に `text` を送り、サーバで `text-embedding` を作成 → `submission(embed)` に保存 → 類似上位3件を返却。
* インデックスは **HNSW**。([GitHub][3])

（Go側の**疑似実装**はCursorに補完させる：`/v1/submission` で `text` を受け→ OpenAI Embeddings → `INSERT` → `SELECT ... ORDER BY embed <=> :q LIMIT 3`）

---

## 7. テーマ切替（毎正時）

* **Cloud Scheduler** から毎時 `POST /v1/theme/rotate` を叩く（ローカルでは任意にボタンで実行）。
* Web（`/`）ではWS or SSEで配信して色・タイトルを切替。MVPは **ポーリング**でも可。
* 「24hリングUI」は後続PRで。

---

## 8. 起動・動作確認（ローカル）

```sh
# DB
docker compose up -d

# API
(cd services/api && go run .)

# Web
(cd apps/web && pnpm dev)

# ブラウザ: http://localhost:3000
# 「接続」→ マイク許可 → AIの音声応答が返ってくればOK
```

---

## 9. 監視・ガードレール

* **鍵取り扱い**：標準APIキーは**サーバのみ**。クライアントは**ephemeral**（TTL\~1分）でWebRTC。([Microsoft Learn][7])
* **プロンプト**：**GAモデルは指示追従が強化**。過度な命令は逆効果になり得るため、放送トーンを明示。([OpenAI Developers][4])
* **温度**：**GAではtemperature廃止**（βとは差異）。**プロンプト側で制御**。([OpenAI Developers][4])

---

## 10. デプロイ（Cloud Run）

### 10.1 Go API

```sh
# 事前に gcloud auth, project設定
gcloud builds submit --tag gcr.io/$PROJECT_ID/radio24-api:latest .
gcloud run deploy radio24-api \
  --image gcr.io/$PROJECT_ID/radio24-api:latest \
  --region asia-northeast1 \
  --allow-unauthenticated \
  --set-env-vars OPENAI_API_KEY=sk-xxx,OPENAI_REALTIME_MODEL=gpt-realtime,OPENAI_REALTIME_VOICE=marin
```

### 10.2 Web（Next.js）

`infra/docker/web.Dockerfile`（最小）：

```dockerfile
FROM node:20 as build
WORKDIR /app
COPY apps/web ./apps/web
WORKDIR /app/apps/web
RUN npm i -g pnpm && pnpm i && pnpm build

FROM gcr.io/distroless/nodejs20
ENV PORT=3000
EXPOSE 3000
WORKDIR /app
COPY --from=build /app/apps/web/.next ./.next
COPY --from=build /app/apps/web/package.json ./package.json
CMD ["./node_modules/next/dist/bin/next","start","-p","3000"]
```

```sh
gcloud builds submit --tag gcr.io/$PROJECT_ID/radio24-web:latest -f infra/docker/web.Dockerfile .
gcloud run deploy radio24-web \
  --image gcr.io/$PROJECT_ID/radio24-web:latest \
  --region asia-northeast1 \
  --allow-unauthenticated \
  --set-env-vars NEXT_PUBLIC_API_BASE="https://radio24-api-xxxxx-an.a.run.app",NEXT_PUBLIC_OPENAI_REALTIME_MODEL="gpt-realtime"
```

---

## 11. 追加の“映える”要素（時間があれば）

* **SIP入電デモ**：Twilio等と連携して**電話が鳴るラジオ**に（RealtimeがSIPを公式サポート）。([OpenAI][1])
* **MCPツール**：天気/ニュース/社内FAQをMCP経由でツール化（GAでは**非同期FC**が改善）。([OpenAI][1])
* **字幕の精度向上**：`response.output_text.delta`を蓄積し行単位で確定表示（Azureのイベント表現が参考）。([Microsoft Learn][8])

---

## 12. Cursorへの発注テンプレ（そのまま貼り付け可）

**Task 1 — リポジトリと環境の雛形を作成**

* 上記ディレクトリ構成を作成し、`docker-compose.yml` と `db/init/001_init.sql`, `db/migrations/002_schema.sql` を配置。
* `apps/web` を Next.js 15 + React 19 で初期化。Chakra Provider を組み込む。
* `services/api` に Go モジュール初期化、`main.go` に `/v1/realtime/ephemeral` を実装。
* `infra/docker/api.Dockerfile` と `infra/docker/web.Dockerfile` を作成。
* **コミット**：`chore: scaffold repo for Radio-24 (web/api/db/infra)`。

**Task 2 — WebRTC接続の実装（ON AIR）**

* `app/page.tsx` に掲載の骨子を実装。
* `NEXT_PUBLIC_API_BASE` を使って`/v1/realtime/ephemeral`を叩き、**WebRTC→Realtime** を確立。
* `session.update` で **voice=marin**、**server\_vad** を設定、`response.create` で初回挨拶。
* `response.output_text.delta` を字幕として表示。
* **コミット**：`feat(web): on-air page with realtime webrtc (audio+subtitles)`。

**Task 3 — 投稿保存 & 類似レコメンド**

* Webに簡易フォーム `/submit` を追加（テキストのみ）。
* API `/v1/submission`：受信テキスト→OpenAI Embeddings→`submission` へ保存→ベクトル近傍3件を返す。
* Webで「今夜の話題」欄に近傍3件を表示。
* **コミット**：`feat(api/web): submission with pgvector-based recommendations`。

**Task 4 — 毎正時テーマ切替**

* API `/v1/theme/rotate` を実装（当面はメモリ or DB保存）。
* WebはSSE/WSまたはポーリングでテーマ受信→背景色/タイトル切替。
* **コミット**：`feat(api/web): hourly theme rotation`。

**Task 5 — Docker化 & Cloud Run デプロイ**

* 2つのサービス（web/api）をCloud Runにデプロイ。
* 環境変数を設定（OPENAI\_API\_KEY など）。
* Webの `NEXT_PUBLIC_API_BASE` をAPIのURLに設定。
* **コミット**：`chore(infra): deploy to Cloud Run`。

**Task 6 — ピッチ用仕上げ**

* `/clips` にダミーの「名場面24連」ギャラリー（後で自動生成に差替）。
* トップに「ON AIR」「投稿」「名場面」へのCTAボタン追加。
* **コミット**：`feat(web): demo polish for pitch`。

---

## 13. 参考（重要な仕様の根拠）

* **Realtime GA & `gpt‑realtime`、`/v1/realtime/client_secrets`、Marin/Cedar、SIP/MCP**：公式発表。([OpenAI][1])
* **GAでのインターフェース変更点（`session.update`、温度廃止、server\_vad/idle\_timeoutなど）**：開発者ノート。([OpenAI Developers][4])
* **WebRTC接続のSDP交換（`application/sdp` POST→answer適用）**：公式ブログのコード例。([OpenAI][5])
* **Ephemeralキーはサーバで発行→クライアントへ配布（TTL\~1分）**：AzureのRealtime解説（概念はOpenAIと同等）。([Microsoft Learn][7])
* **pgvectorのHNSWインデックス**：公式GitHub/リリースノート。([GitHub][3])
* **Agents SDKのWebRTCトランスポート（利用可）**：ドキュメント。([OpenAI GitHub Pages][9])

---

### メモ（実装のコツ）

* **マイク取り扱い**：`getUserMedia({audio:true})` は早めに要求してUXを安定させる。
* **字幕**：`response.output_text.delta` を蓄積して、`response.completed` で確定する設計が扱いやすい。([Microsoft Learn][8])
* **声・話速・トーン**は\*\*プロンプト/`session.update`\*\*で調整（GAでは温度ではなく指示最適化）。([OpenAI Developers][4])
* **セキュリティ**：鍵は**絶対にクライアント配布しない**。**ephemeral**のみ渡す。([Microsoft Learn][7])

---

[1]: https://openai.com/index/introducing-gpt-realtime/ "Introducing gpt-realtime and Realtime API updates for production voice agents | OpenAI"
[2]: https://nextjs.org/blog/next-15?utm_source=chatgpt.com "Next.js 15"
[3]: https://github.com/pgvector/pgvector?utm_source=chatgpt.com "pgvector/pgvector: Open-source vector similarity search for ..."
[4]: https://developers.openai.com/blog/realtime-api/ "Developer notes on the Realtime API"
[5]: https://openai.com/index/o1-and-new-tools-for-developers/?utm_source=chatgpt.com "OpenAI o1 and new tools for developers"
[6]: https://webrtchacks.com/the-unofficial-guide-to-openai-realtime-webrtc-api/?utm_source=chatgpt.com "The Unofficial Guide to OpenAI Realtime WebRTC API"
[7]: https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/realtime-audio-webrtc?utm_source=chatgpt.com "How to use the GPT Realtime API via WebRTC - Azure ..."
[8]: https://learn.microsoft.com/en-us/azure/ai-foundry/openai/realtime-audio-reference?utm_source=chatgpt.com "Audio events reference - Azure OpenAI"
[9]: https://openai.github.io/openai-agents-js/guides/voice-agents/transport/?utm_source=chatgpt.com "Realtime Transport Layer | OpenAI Agents SDK - GitHub Pages"
