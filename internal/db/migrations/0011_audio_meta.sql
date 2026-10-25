-- +goose Up
-- Audio metadata of the first audio track, probed by the preview worker and
-- the boot backfill. NULL = never probed, '' = probed, no audio. Firefox
-- cannot decode pcm_* in MP4/MKV: the client plays those tracks through Web
-- Audio from /api/clips/{id}/pcm instead of the <video> element.
ALTER TABLE clips ADD COLUMN audio_codec TEXT;
ALTER TABLE clips ADD COLUMN audio_rate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE clips ADD COLUMN audio_channels INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE clips DROP COLUMN audio_channels;
ALTER TABLE clips DROP COLUMN audio_rate;
ALTER TABLE clips DROP COLUMN audio_codec;
