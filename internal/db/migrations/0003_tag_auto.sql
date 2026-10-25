-- +goose Up
-- +goose StatementBegin
ALTER TABLE tags ADD COLUMN auto INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_tags_auto ON tags(auto);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tags_auto;
ALTER TABLE tags DROP COLUMN auto;
-- +goose StatementEnd
