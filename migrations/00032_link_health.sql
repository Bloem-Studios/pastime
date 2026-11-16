-- +goose Up
ALTER TABLE service_links ADD COLUMN health text NOT NULL DEFAULT 'unknown' CHECK(health IN ('unknown','healthy','credentials_rejected','unavailable','rate_limited','not_configured','invalid_response'));
ALTER TABLE service_links ADD COLUMN checked_at timestamptz;
