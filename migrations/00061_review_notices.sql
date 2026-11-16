-- Year in Review notices: once a year Pastime tells the customer system (Garden)
-- how each customer's year went, and Garden emails them. One row per account and
-- year keeps the numbers that were sent, so a retry sends the same ones, and
-- records what happened.
-- +goose Up
CREATE TABLE review_notices (
    account_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    year integer NOT NULL CHECK (year BETWEEN 2000 AND 9999),
    subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
    minutes integer NOT NULL CHECK (minutes BETWEEN 0 AND 100000000),
    titles integer NOT NULL CHECK (titles BETWEEN 0 AND 1000000),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'opted_out', 'unknown', 'refused', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz,
    last_error text CHECK (length(last_error) <= 300),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (account_id, year)
);
CREATE INDEX review_notices_pending ON review_notices (year, account_id) WHERE status = 'pending';

-- +goose Down
DROP TABLE review_notices;
