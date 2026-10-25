-- +goose Up
ALTER TABLE clips ADD COLUMN audio_track_count INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE clips DROP COLUMN audio_track_count;
