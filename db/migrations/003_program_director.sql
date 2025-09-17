-- Program Director用テーブル
-- CHANNEL: 将来チャンネル増やす下地（音楽枠/英語枠など）
CREATE TABLE IF NOT EXISTS channel (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    -- "Radio-24"
    live BOOLEAN DEFAULT true,
    -- 常時 true
    started_at TIMESTAMPTZ DEFAULT now()
);
-- SCHEDULE: Program Directorの台本/ガイダンスの時間割
CREATE TABLE IF NOT EXISTS schedule (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID REFERENCES channel(id) ON DELETE CASCADE,
    hour INTEGER CHECK (
        hour >= 0
        AND hour <= 23
    ),
    -- 0..23
    block TEXT CHECK (
        block IN (
            'OP',
            'NEWS',
            'QANDA',
            'MUSIC',
            'TOPIC_A',
            'JINGLE'
        )
    ) NOT NULL,
    prompt TEXT,
    -- 進行用ガイダンス
    created_at TIMESTAMPTZ DEFAULT now()
);
-- QUEUE: PTT入力の優先度と状態管理
CREATE TABLE IF NOT EXISTS queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT,
    kind TEXT CHECK (kind IN ('audio', 'text', 'phone')) NOT NULL,
    text TEXT,
    -- transcript
    meta JSONB,
    -- priority, phoneNumber等
    enqueued_at TIMESTAMPTZ DEFAULT now(),
    status TEXT CHECK (status IN ('queued', 'live', 'done', 'dropped')) DEFAULT 'queued'
);
-- デフォルトチャンネルを作成
INSERT INTO channel (name, live)
VALUES ('Radio-24', true) ON CONFLICT (name) DO NOTHING;
-- デフォルトスケジュールを作成（24時間分）
INSERT INTO schedule (channel_id, hour, block, prompt)
SELECT c.id,
    h.hour,
    CASE
        WHEN h.hour BETWEEN 0 AND 5 THEN 'MUSIC'
        WHEN h.hour BETWEEN 6 AND 8 THEN 'NEWS'
        WHEN h.hour BETWEEN 9 AND 11 THEN 'TOPIC_A'
        WHEN h.hour BETWEEN 12 AND 14 THEN 'QANDA'
        WHEN h.hour BETWEEN 15 AND 17 THEN 'MUSIC'
        WHEN h.hour BETWEEN 18 AND 20 THEN 'NEWS'
        WHEN h.hour BETWEEN 21 AND 23 THEN 'TOPIC_A'
    END as block,
    CASE
        WHEN h.hour BETWEEN 0 AND 5 THEN '深夜の音楽を流しながら、静かに語りかけましょう。'
        WHEN h.hour BETWEEN 6 AND 8 THEN '朝のニュースを分かりやすく伝え、一日の始まりを応援しましょう。'
        WHEN h.hour BETWEEN 9 AND 11 THEN '午前のトピックについて、リスナーと一緒に考えましょう。'
        WHEN h.hour BETWEEN 12 AND 14 THEN 'リスナーからの質問に答える時間です。'
        WHEN h.hour BETWEEN 15 AND 17 THEN '午後の音楽でリラックスした時間を提供しましょう。'
        WHEN h.hour BETWEEN 18 AND 20 THEN '夕方のニュースで一日を振り返りましょう。'
        WHEN h.hour BETWEEN 21 AND 23 THEN '夜のトピックで深く語り合いましょう。'
    END as prompt
FROM channel c
    CROSS JOIN (
        SELECT generate_series(0, 23) as hour
    ) h
WHERE c.name = 'Radio-24' ON CONFLICT DO NOTHING;
-- インデックス作成
CREATE INDEX IF NOT EXISTS idx_schedule_channel_hour ON schedule(channel_id, hour);
CREATE INDEX IF NOT EXISTS idx_queue_status_enqueued ON queue(status, enqueued_at);
CREATE INDEX IF NOT EXISTS idx_queue_meta_priority ON queue USING GIN (meta);