-- +goose Up
ALTER TABLE categories ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;
ALTER TABLE categories ADD COLUMN image_url TEXT;
ALTER TABLE clips ADD COLUMN fps_num INTEGER;
ALTER TABLE clips ADD COLUMN fps_den INTEGER;

UPDATE categories SET hidden = 1 WHERE slug = 'allstar';

DELETE FROM clip_tags WHERE tag_id IN (SELECT id FROM tags WHERE auto = 1);
DELETE FROM tags WHERE auto = 1;

-- +goose Down
DELETE FROM tags WHERE auto = 1;
ALTER TABLE clips DROP COLUMN fps_den;
ALTER TABLE clips DROP COLUMN fps_num;
ALTER TABLE categories DROP COLUMN image_url;
ALTER TABLE categories DROP COLUMN hidden;
