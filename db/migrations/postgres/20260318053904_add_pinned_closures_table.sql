-- migrate:up
CREATE TABLE pinned_closures (
    id BIGSERIAL PRIMARY KEY,
    hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ
);

-- migrate:down
DROP TABLE IF EXISTS pinned_closures;
