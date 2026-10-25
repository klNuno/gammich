-- +goose Up
-- +goose StatementBegin

-- Renaming a tag changes tag_names for every clip carrying it. Contentless
-- fts5 forbids UPDATE, so delete then re-insert each affected (non-deleted)
-- clip. (Membership changes are already handled by trg_clip_tags_*.)
CREATE TRIGGER trg_tags_fts_rename AFTER UPDATE OF name ON tags
BEGIN
  DELETE FROM clips_fts
  WHERE rowid IN (SELECT clip_id FROM clip_tags WHERE tag_id = NEW.id);
  INSERT INTO clips_fts (rowid, name, original_filename, category_name, tag_names)
  SELECT c.id, c.name, c.original_filename,
         (SELECT name FROM categories WHERE id = c.category_id),
         COALESCE((SELECT GROUP_CONCAT(t.name, ' ')
                   FROM clip_tags ct JOIN tags t ON t.id = ct.tag_id
                   WHERE ct.clip_id = c.id), '')
  FROM clips c
  WHERE c.deleted_at IS NULL
    AND c.id IN (SELECT clip_id FROM clip_tags WHERE tag_id = NEW.id);
END;

-- Renaming a category changes category_name for its clips.
CREATE TRIGGER trg_categories_fts_rename AFTER UPDATE OF name ON categories
BEGIN
  DELETE FROM clips_fts
  WHERE rowid IN (SELECT id FROM clips WHERE category_id = NEW.id AND deleted_at IS NULL);
  INSERT INTO clips_fts (rowid, name, original_filename, category_name, tag_names)
  SELECT c.id, c.name, c.original_filename, NEW.name,
         COALESCE((SELECT GROUP_CONCAT(t.name, ' ')
                   FROM clip_tags ct JOIN tags t ON t.id = ct.tag_id
                   WHERE ct.clip_id = c.id), '')
  FROM clips c
  WHERE c.category_id = NEW.id AND c.deleted_at IS NULL;
END;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_categories_fts_rename;
DROP TRIGGER IF EXISTS trg_tags_fts_rename;
-- +goose StatementEnd
