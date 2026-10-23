#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set app_password="$APP_DATABASE_PASSWORD" <<'SQL'
CREATE ROLE agent_platform LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'app_password';
GRANT CONNECT ON DATABASE agent_platform TO agent_platform;
GRANT USAGE, CREATE ON SCHEMA public TO agent_platform;
SQL
