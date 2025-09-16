# Radio-24 — v2 ブロードキャスト化 実装ブリーフ（Cursor用）

**テーマ**：24時間AIラジオ局「Radio-24」
**スタック**：Next.js 15 + React 19、Go(API)、PostgreSQL(+pgvector)、Cloud Run
**音声**：OpenAI Realtime API（**サーバ常駐1セッション**）＋ **SFU配信（LiveKit）**

---

## ゴール（Definition of Done）

* サーバ側の **Host Agent（AIパーソナリティ）** が **24時間発話**し続け、**LiveKit** に音声を **Publish**。
* 視聴者（ブラウザ）は **LiveKit** に **Subscribe** するだけで **現在の放送に合流**（OpenAIへは直結しない）。
* 視聴者の **PTT投稿** は API に集約 → **Queue** → \*\*割り込み（ダッキング）\*\*付きで Host に順次注入。
* **毎正時テーマ** が自動切替（UIの色とNowPlaying反映）。
* v1で実装済みの **投稿保存＋pgvectorレコメンド** は継続利用。

---

## 差分サマリ（v1 → v2）

* ❌ クライアント毎に Realtime へ WebRTC 直結 → **廃止**
* ✅ **サーバ常駐の Realtime 1セッション**（Host 用）を新設
* ✅ 視聴者は **LiveKit(Room) に Join**（Subscribe only トークン）
* ✅ API は **OpenAI Ephemeral** の代わりに **LiveKit Join Token** を発行
* ✅ **Mixer（ダッキング）** と **Program Director（時報/進行）** をサーバに追加

---

## ディレクトリ構成（追加/変更）

```
radio-24/
├─ apps/
│  └─ web/
├─ services/
│  ├─ api/                    # Go: REST/WS、Queue、Director、Mixer制御
│  └─ host/                   # Go or Node: Host Agent（OpenAI Realtimeへ接続）
├─ pkg/
│  ├─ director/               # 番組進行ステートマシン
│  ├─ mixer/                  # ダッキング制御IF
│  └─ queue/                  # PTTキュー
├─ db/
│  ├─ migrations/
│  └─ init/
├─ infra/
│  ├─ docker/
│  ├─ cloudrun/
│  └─ livekit/                # LiveKit設定（ローカル用docker-compose or Cloud）
├─ docs/
│  └─ cursor-setup-radio24-v2.md   # ← 本書
└─ docker-compose.yml
```

---

## .env（追記）

```
# LiveKit
LIVEKIT_API_KEY=lk_***
LIVEKIT_API_SECRET=lksec_***
LIVEKIT_WS_URL=wss://your-livekit.example.com

# OpenAI（サーバ常駐Host用）
OPENAI_API_KEY=sk-***
OPENAI_REALTIME_MODEL=gpt-realtime
OPENAI_REALTIME_VOICE=marin

# API
API_PORT=8080
ALLOWED_ORIGIN=http://localhost:3000
```

> v2ではブラウザに **OpenAI鍵を一切渡さない**。ブラウザは **LiveKit Join Token** のみ取得。

---

## 実装タスク一覧（順番厳守）

### Task 1 — LiveKit 環境 & SDK 追加

* `infra/livekit/` にローカル検証用の LiveKit サーバ設定（docker-compose 版）を追加。
* Web に LiveKit クライアント SDK 追加（`livekit-client`）。
* API で **Join Token 発行**を行うために `LIVEKIT_API_KEY/SECRET` を使うユーティリティを実装。

**Files**

* `infra/livekit/docker-compose.yml`（ローカル起動用・既定値でOK）
* `services/api/internal/livekit/token.go`（Joinトークン生成）

**API**

```http
POST /v1/room/join
- body: { identity: string }  // 任意のユーザID
- res : { url: string, token: string, room: "radio-24" }
```

### Task 2 — Host Agent（常時発話）の最小実装

* `services/host/` に Host プロセス（Go or Node）。
* 起動時に **OpenAI Realtime** へ WebRTC または WebSocket で接続。
* 受け取った **音声出力** を **LiveKit に Publish**（Server SDKまたはエージェントSDKを利用）。
* **Program Director** からの指示（ガイダンス、時報、ジングル）を受けて **無音なく発話**。

**要件**

* 切断時は自動再接続。
* `voice=marin`、`server_vad` は Host側で制御。
* 初回起動後、30秒ごとに軽い話題 or ステーションIDを挟み無音を回避。

### Task 3 — Program Director（番組進行）

* `pkg/director/`：有限状態機械（`OP -> TOPIC_A -> QANDA -> JINGLE -> ...`）。
* **毎正時**にテーマ切替、**15分刻み**のセグメント進行。
* Host へ **進行プロンプト** を送るIFを用意（gRPCでも関数でもOK）。
* API から `GET /v1/now` で **NowPlaying** 情報を取得できるようにする。

**API**

```http
GET  /v1/now           // { theme, segment, nextTickAt, listeners }
POST /v1/admin/advance // デモ用：状態を手動で進める
```

### Task 4 — PTT Queue（投稿割り込み）

* Web からの PTT/テキスト投稿を **API** 経由で受けて **Queue** に積む。
* Director が `segment=QANDA` のときだけ **DEQUEUE** して Host にインジェクト。
* Mixer に **ダッキングON/OFF** を発行（-12〜-18dB推奨）。

**API/WS**

```http
WS  /ws/ptt
- client -> server: { type:"ptt", kind:"audio"|"text", text? }
- server -> director/host: EVENT.PTT_ENQUEUED / DEQUEUED

POST /v1/submission     // 既存：投稿保存＋埋め込み→近傍3件返却（変更なし）
```

### Task 5 — Mixer（ダッキング）

* `pkg/mixer/`：Host音声を基準に、**割り込み時のみBGM/Hostを自動減衰**。
* 実装パスは2択：

  1. **LiveKit Serverサイド**（RoomComposite/Server Media API）
  2. **サーバ内 GStreamer/FFmpeg** で合成して **単一トラック**を Publish
* v2では **単一配信トラック** を優先（録音・クリップ作成が容易）。

**内部イベント**

```
EVENT.MIXER_DUCK_ON
EVENT.MIXER_DUCK_OFF
```

### Task 6 — Web（Listener合流UI）

* `/on-air`：

  * `POST /v1/room/join` を叩いて `{url, token}` を取得 → LiveKit へ **Subscribe 接続**。
  * 受信音声は即自動再生。
  * PTTボタンは **API/WSへ送信**（ブラウザからOpenAIへは送らない）。
  * NowPlaying（`GET /v1/now` or SSE/WS）でテーマ・視聴者数・字幕（要約）を表示。

* `/submit`：既存通り（テキスト投稿→pgvector 近傍3件表示）

---

## Web 実装テンプレ（抜粋・Cursorに最終化させる）

```tsx
// apps/web/app/on-air/page.tsx
'use client';

import { useEffect, useState } from 'react';
import { Room, RoomEvent, RemoteParticipant, RemoteTrackPublication, RemoteAudioTrack, connect } from 'livekit-client';

export default function OnAir() {
  const [joined, setJoined] = useState(false);
  const [now, setNow] = useState<{theme:string; segment:string} | null>(null);

  async function join() {
    const res = await fetch(process.env.NEXT_PUBLIC_API_BASE + '/v1/room/join', { method:'POST', body: JSON.stringify({identity: crypto.randomUUID()}), headers:{'Content-Type':'application/json'}});
    const { url, token } = await res.json();

    const room = await connect(url, token, { audio: true, video: false });
    room.on(RoomEvent.TrackSubscribed, (_t, pub, _p) => {
      const track = (pub as RemoteTrackPublication).track as RemoteAudioTrack;
      if (track) track.attach(); // 自動再生
    });

    setJoined(true);
    pollNowPlaying();
  }

  async function pollNowPlaying() {
    const r = await fetch(process.env.NEXT_PUBLIC_API_BASE + '/v1/now');
    setNow(await r.json());
    setTimeout(pollNowPlaying, 5000);
  }

  async function pttDown() {
    // 音声はWSで送る。MVPはテキストでもOK
    // fetch('/ws/ptt') ではなく、専用WSクライアントを実装
  }
  async function pttUp() {}

  return (
    <main className="p-6 space-y-4">
      <h1>Radio-24 — ON AIR</h1>
      {!joined ? <button onClick={join}>合流</button> :
        <div className="flex gap-3 items-center">
          <button onMouseDown={pttDown} onMouseUp={pttUp}>🎙️ PTT</button>
          <span>{now ? `${now.theme} / ${now.segment}` : '...'}</span>
        </div>
      }
      <div id="subtitles" className="min-h-16 text-sm opacity-80"></div>
    </main>
  );
}
```

---

## API 実装ポイント（Go）

### 1) LiveKit Join Token

```go
// POST /v1/room/join
// req: {identity string} -> res: {url, token, room}
```

* LiveKit公式のGo SDK or JWT生成で実装。`room="radio-24"`, `subscribeOnly=true` 相当の権限付与。

### 2) PTT WS

```go
// WS /ws/ptt
// client -> {type:"ptt", kind:"text"|"audio", text?}
// - audioは別途opus/WebMフレームで送信 → サーバ側でASR → transcript
// - queue.Enqueue(transcript, priority)
```

* `pkg/queue` に `Enqueue/Dequeue`。
* `pkg/director` が `segment==QANDA` の時に `Dequeue`→ Host へ渡す。
* 受け渡しは in-proc channel or Redis Stream でもよい（Cloud Run可用性を意識）。

### 3) Director Tick

```go
// cron or internal ticker (1s)
// - Top of hour -> switch theme
// - 15min segment advance
// - push nowPlaying to cache
```

### 4) Mixer IF

```go
// pkg/mixer: DuckOn(), DuckOff()
// LiveKitのServer API/RoomComposite か GStreamer 経由で実装
```

---

## Host Agent（概念：Go or Node）

* Realtime へ接続（WS or WebRTC）。
* **input**：Directorのガイダンス、Queueの質問
* **output**：音声（Marin）、テキスト（字幕要約）
* 出力音声を LiveKit Publish（サーバサイド）
* 切断時はバックオフで再接続。エラー時は「機材トラブル」アナウンス。

**最初の `session.update` 例（送信側で実装）**

```json
{
  "type": "session.update",
  "session": {
    "type": "realtime",
    "instructions": "あなたは24時間AIラジオのDJ。無音禁止、短文でテンポよく。Q&Aでは回答→10文字要約→次へ。",
    "voice": "marin",
    "audio": { "input": { "turn_detection": { "type": "server_vad", "idle_timeout_ms": 6000 } } }
  }
}
```

---

## DB 変更（最小）

* 既存の `submission` 継続利用。
* 追加するなら：

```sql
CREATE TABLE IF NOT EXISTS ptt_queue (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT,
  kind TEXT CHECK (kind IN ('text','audio','phone')) NOT NULL,
  text TEXT,
  priority INT DEFAULT 0,
  status TEXT DEFAULT 'queued',
  enqueued_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS schedule (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  hour INT,
  block TEXT,           -- OP/NEWS/QANDA/JINGLE...
  prompt TEXT
);
```

---

## 動作確認（ローカル）

```bash
# 1) LiveKit（ローカル）起動
docker compose -f infra/livekit/docker-compose.yml up -d

# 2) DB
docker compose up -d db

# 3) API
(cd services/api && go run .)

# 4) Host Agent
(cd services/host && go run .)  # or node .

# 5) Web
(cd apps/web && pnpm dev)

# ブラウザ http://localhost:3000/on-air
# -> 合流ボタンで現在の放送に即接続
# -> PTTで投稿→Q&A割り込み（Hostが応答、ダッキング）
```

---

## デプロイ（Cloud Run）

* `services/api`, `services/host` を **別サービス** としてデプロイ。
* LiveKit はマネージド or コンテナ自前運用。
* 環境変数：`LIVEKIT_*`, `OPENAI_*` をそれぞれのサービスに設定。
* Web の `NEXT_PUBLIC_API_BASE` を API の URL に。

---

## 品質/運用

* **無音監視**：Host 出力の無音を 10–15秒で検知 → 自動ジングル/ニュース差し込み。
* **フォールバック**：Realtime再接続中はTTSでステーションIDをループ。
* **録音/名場面**：単一トラックをサーバで録音→1分ダイジェスト自動生成。
* **モデレーション**：PTT/テキストに簡易NGワード、PII抑止。

---

## マイグレーション手順（v1→v2）

1. `services/host` を追加して **常時発話**を確立。
2. `POST /v1/realtime/ephemeral` を **非推奨化**（当面残しても可）。
3. Web の `app/on-air/page.tsx` を **LiveKit Subscribe** 実装に差し替え。
4. `PTT` は `/ws/ptt` へ送出。
5. `director` の Tick と `GET /v1/now` を導入し UI 連動。
6. Cloud Run 2サービス（api/host）＋ LiveKit を本番配置。

---

## Cursor 実施コマンド（コミット粒度）

1. `chore: add livekit infra and token issuer (api)`
2. `feat(host): realtime always-on agent publishing to livekit`
3. `feat(pkg): director state machine & nowPlaying api`
4. `feat(api): ptt ws + queue + mixer hooks`
5. `feat(web): on-air page subscribing via livekit + ptt ui`
6. `chore(infra): cloud run deploy (api/host), env wiring`
7. `feat(web): hourly theme ui & demo polish`

---

### 付録：Director → Host 連携ガイド（簡易）

* Directorは現在の `theme/segment` と `top3 PTT` を **要約テキスト**で Host に渡す。
* Hostは **「短い前振り → 回答 → 10文字要約（字幕）」** の順で喋る。
* `QANDA` 以外は PTT を **キューに溜めるだけ**（割り込み禁止）。

---

