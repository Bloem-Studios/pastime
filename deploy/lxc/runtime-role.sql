-- Run after migrations, as the database owner. psql variable runtime_password is
-- supplied on stdin by the installer, never committed or passed in process args.
\set ON_ERROR_STOP on
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='pastime_app') THEN
  CREATE ROLE pastime_app LOGIN;
 END IF;
END $$;
ALTER ROLE pastime_app WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT PASSWORD :'runtime_password';
SELECT current_database() AS db \gset
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE :"db" TO pastime_app;
GRANT USAGE ON SCHEMA public TO pastime_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO pastime_app;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO pastime_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO pastime_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE,SELECT ON SEQUENCES TO pastime_app;
REVOKE ALL ON TABLE goose_db_version FROM pastime_app;
GRANT SELECT ON TABLE goose_db_version TO pastime_app;
