-- +goose Up
ALTER TABLE service_links DROP CONSTRAINT service_links_schedule_check;
ALTER TABLE service_links ADD CONSTRAINT service_links_schedule_check CHECK(schedule IN ('manual','hourly','two_hourly','daily','weekly'));
