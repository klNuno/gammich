-- +goose Up
-- +goose StatementBegin

-- Populate clips_fts from existing rows.
INSERT INTO clips_fts (rowid, name, original_filename, category_name, tag_names)
SELECT c.id,
       c.name,
       c.original_filename,
       COALESCE(cat.name, ''),
       COALESCE((
         SELECT GROUP_CONCAT(t.name, ' ')
         FROM clip_tags ct JOIN tags t ON t.id = ct.tag_id
         WHERE ct.clip_id = c.id
       ), '')
FROM clips c
LEFT JOIN categories cat ON cat.id = c.category_id
WHERE c.deleted_at IS NULL;

-- Insert: index when a clip is inserted.
CREATE TRIGGER trg_clips_fts_ins AFTER INSERT ON clips
BEGIN
  INSERT INTO clips_fts (rowid, name, original_filename, category_name, tag_names)
  VALUES (NEW.id, NEW.name, NEW.original_filename,
          (SELECT name FROM categories WHERE id = NEW.category_id),
          '');
END;

-- Soft-delete: remove from index.
CREATE TRIGGER trg_clips_fts_del AFTER UPDATE OF deleted_at ON clips
WHEN NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL
BEGIN
  DELETE FROM clips_fts WHERE rowid = NEW.id;
END;

-- Restore: re-index if deleted_at goes back to NULL.
CREATE TRIGGER trg_clips_fts_restore AFTER UPDATE OF deleted_at ON clips
WHEN NEW.deleted_at IS NULL AND OLD.deleted_at IS NOT NULL
BEGIN
  INSERT INTO clips_fts (rowid, name, original_filename, category_name, tag_names)
  VALUES (NEW.id, NEW.name, NEW.original_filename,
          (SELECT name FROM categories WHERE id = NEW.category_id),
          COALESCE((SELECT GROUP_CONCAT(t.name, ' ')
                    FROM clip_tags ct JOIN tags t ON t.id = ct.tag_id
                    WHERE ct.clip_id = NEW.id), ''));
END;

-- Re-index on name / category change (contentless fts5 forbids UPDATE,
-- so we delete then re-insert).
CREATE TRIGGER trg_clips_fts_upd_name AFTER UPDATE OF name, category_id ON clips
WHEN NEW.deleted_at IS NULL
BEGIN
  DELETE FROM clips_fts WHERE rowid = NEW.id;
  INSERT INTO clips_fts (rowid, name, original_filename, category_name, tag_names)
  VALUES (NEW.id, NEW.name, NEW.original_filename,
          (SELECT name FROM categories WHERE id = NEW.category_id),
          COALESCE((SELECT GROUP_CONCAT(t.name, ' ')
                    FROM clip_tags ct JOIN tags t ON t.id = ct.tag_id
                    WHERE ct.clip_id = NEW.id), ''));
END;

-- Maintain tag_names whenever clip_tags membership changes.
CREATE TRIGGER trg_clip_tags_ins AFTER INSERT ON clip_tags
BEGIN
  DELETE FROM clips_fts WHERE rowid = NEW.clip_id;
  INSERT INTO clips_fts (rowid, name, original_filename, category_name, tag_names)
  SELECT c.id, c.name, c.original_filename,
         (SELECT name FROM categories WHERE id = c.category_id),
         COALESCE((SELECT GROUP_CONCAT(t.name, ' ')
                   FROM clip_tags ct JOIN tags t ON t.id = ct.tag_id
                   WHERE ct.clip_id = c.id), '')
  FROM clips c WHERE c.id = NEW.clip_id AND c.deleted_at IS NULL;
END;

CREATE TRIGGER trg_clip_tags_del AFTER DELETE ON clip_tags
BEGIN
  DELETE FROM clips_fts WHERE rowid = OLD.clip_id;
  INSERT INTO clips_fts (rowid, name, original_filename, category_name, tag_names)
  SELECT c.id, c.name, c.original_filename,
         (SELECT name FROM categories WHERE id = c.category_id),
         COALESCE((SELECT GROUP_CONCAT(t.name, ' ')
                   FROM clip_tags ct JOIN tags t ON t.id = ct.tag_id
                   WHERE ct.clip_id = c.id), '')
  FROM clips c WHERE c.id = OLD.clip_id AND c.deleted_at IS NULL;
END;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_clip_tags_del;
DROP TRIGGER IF EXISTS trg_clip_tags_ins;
DROP TRIGGER IF EXISTS trg_clips_fts_upd_name;
DROP TRIGGER IF EXISTS trg_clips_fts_restore;
DROP TRIGGER IF EXISTS trg_clips_fts_del;
DROP TRIGGER IF EXISTS trg_clips_fts_ins;
DELETE FROM clips_fts;
-- +goose StatementEnd
