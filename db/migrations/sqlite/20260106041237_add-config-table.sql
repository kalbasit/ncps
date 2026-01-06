-- migrate:up
CREATE TABLE "config" (
    "id" INTEGER PRIMARY KEY AUTOINCREMENT,
    "key" TEXT NOT NULL UNIQUE,
    "value" TEXT NOT NULL,
    "created_at" TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    "updated_at" TIMESTAMP
);

-- migrate:down
DROP TABLE "config";
