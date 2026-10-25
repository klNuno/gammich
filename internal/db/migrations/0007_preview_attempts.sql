-- +goose Up
ALTER TABLE clips ADD COLUMN preview_attempts INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE clips DROP COLUMN preview_attempts;
