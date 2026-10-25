-- +goose Up
ALTER TABLE clips ADD COLUMN kind TEXT NOT NULL DEFAULT 'video';
CREATE INDEX idx_clips_kind ON clips(kind);

-- +goose Down
DROP INDEX IF EXISTS idx_clips_kind;
ALTER TABLE clips DROP COLUMN kind;
