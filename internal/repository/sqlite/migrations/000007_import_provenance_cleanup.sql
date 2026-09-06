-- +goose Up
UPDATE imports SET file_path = '' WHERE file_path <> '';

-- +goose Down
-- Absolute source paths are intentionally not recoverable.

