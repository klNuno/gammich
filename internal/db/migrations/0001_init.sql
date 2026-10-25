-- +goose Up
-- +goose StatementBegin
CREATE TABLE categories (
  id   INTEGER PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL
);

CREATE TABLE tags (
  id   INTEGER PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL
);

CREATE TABLE clips (
  id                INTEGER PRIMARY KEY,
  rel_path          TEXT    NOT NULL UNIQUE,
  name              TEXT    NOT NULL,
  original_filename TEXT    NOT NULL,
  size_bytes        INTEGER NOT NULL,
  mtime             INTEGER NOT NULL,
  duration_s        REAL,
  width             INTEGER,
  height            INTEGER,
  codec             TEXT,
  category_id       INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  preview_status    TEXT    NOT NULL DEFAULT 'pending',
  preview_error     TEXT,
  deleted_at        INTEGER,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);

CREATE INDEX idx_clips_category   ON clips(category_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_clips_mtime      ON clips(mtime DESC)  WHERE deleted_at IS NULL;
CREATE INDEX idx_clips_deleted_at ON clips(deleted_at);
CREATE INDEX idx_clips_preview    ON clips(preview_status);

CREATE TABLE clip_tags (
  clip_id INTEGER NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
  tag_id  INTEGER NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
  PRIMARY KEY (clip_id, tag_id)
);

CREATE INDEX idx_clip_tags_tag ON clip_tags(tag_id);

CREATE TABLE jobs (
  id         INTEGER PRIMARY KEY,
  type       TEXT    NOT NULL,
  clip_id    INTEGER REFERENCES clips(id) ON DELETE CASCADE,
  status     TEXT    NOT NULL DEFAULT 'queued',
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX idx_jobs_status ON jobs(status, type);

CREATE VIRTUAL TABLE clips_fts USING fts5(
  name,
  original_filename,
  category_name,
  tag_names,
  content='',
  contentless_delete=1
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS clips_fts;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS clip_tags;
DROP TABLE IF EXISTS clips;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS categories;
-- +goose StatementEnd
