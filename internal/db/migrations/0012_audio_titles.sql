-- +goose Up
-- Per-track audio names as a JSON array of strings, one per audio stream, ""
-- for an unnamed one; '[]' when no track carries a name. NULL = never probed
-- for names: BackfillAudio fills multi-track clips.
ALTER TABLE clips ADD COLUMN audio_track_titles TEXT;

-- +goose Down
ALTER TABLE clips DROP COLUMN audio_track_titles;
