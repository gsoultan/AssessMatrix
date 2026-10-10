#!/bin/sh
# Runs once, on an empty data directory. Creates the schema owner (used only
# by `core migrate`) and the application role (owns nothing, cannot bypass
# row-level security; `core serve` refuses to start as anything stronger).
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" \
  -v owner_pw="$AM_OWNER_PASSWORD" -v app_pw="$AM_APP_PASSWORD" <<'SQL'
CREATE ROLE am_owner LOGIN PASSWORD :'owner_pw';
CREATE ROLE am_app LOGIN PASSWORD :'app_pw';
CREATE DATABASE assessmatrix OWNER am_owner;
SQL
