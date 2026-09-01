-- +goose Up
DROP TABLE IF EXISTS tdata_import_batch_items;
DROP TABLE IF EXISTS tdata_import_items;
DROP TABLE IF EXISTS tdata_import_batches;

-- +goose Down
SELECT 1;
