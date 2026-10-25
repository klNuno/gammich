-- +goose Up
-- Screenshots used to go straight to 'done' without a thumb, so the grid served
-- full-size originals. Requeue them so the worker makes their thumbs
-- (one ffmpeg frame, fast).
UPDATE clips SET preview_status = 'pending', preview_attempts = 0
WHERE kind = 'screenshot' AND deleted_at IS NULL;

-- +goose Down
UPDATE clips SET preview_status = 'done'
WHERE kind = 'screenshot' AND preview_status = 'pending';
