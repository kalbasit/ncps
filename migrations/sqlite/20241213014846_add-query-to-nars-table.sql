-- +goose Up
ALTER TABLE nars ADD COLUMN `query` TEXT NOT NULL DEFAULT '';


-- +goose Down
ALTER TABLE nars DROP COLUMN `query`;
