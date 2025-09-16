-- 投稿保存 (簡略)
CREATE TABLE IF NOT EXISTS submission (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT,
    type TEXT CHECK (type IN ('text', 'audio')) NOT NULL,
    text TEXT,
    embed VECTOR(1536),
    -- pgvector
    created_at TIMESTAMPTZ DEFAULT now()
);
-- 類似検索を速くするHNSWインデックス（0.5+）
CREATE INDEX IF NOT EXISTS submission_embed_hnsw ON submission USING hnsw (embed vector_cosine_ops);