-- +goose Up
-- -1 = unknown, 0 = already faststart (moov before mdat), 1 = moov at the end
-- of the file (game recorders): Firefox loses the audio track, served through
-- a virtual faststart layout.
ALTER TABLE clips ADD COLUMN moov_at_end INTEGER NOT NULL DEFAULT -1;

-- +goose Down
ALTER TABLE clips DROP COLUMN moov_at_end;
