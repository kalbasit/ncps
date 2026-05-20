-- +goose Up
CREATE TABLE "pinned_closures" (
    "id" INTEGER PRIMARY KEY AUTOINCREMENT,
    "hash" TEXT NOT NULL UNIQUE,
    "created_at" TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    "updated_at" TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS "pinned_closures";
