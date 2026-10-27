#!/usr/bin/env bash
# Apply all pending migrations against the configured SingleStore cluster.
# Reads SINGLESTORE_* vars from the repo-root .env (gitignored).

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="$repo_root/.env"

if [[ ! -f "$env_file" ]]; then
    echo "Missing $env_file (copy .env.example to .env and fill it in)" >&2
    exit 1
fi

# shellcheck disable=SC1090
set -a; . "$env_file"; set +a

: "${SINGLESTORE_HOST:?SINGLESTORE_HOST not set in .env}"
: "${SINGLESTORE_PORT:?SINGLESTORE_PORT not set in .env}"
: "${SINGLESTORE_USER:?SINGLESTORE_USER not set in .env}"
: "${SINGLESTORE_PASSWORD:?SINGLESTORE_PASSWORD not set in .env}"

for file in "$repo_root"/migrations/*.sql; do
    name="$(basename "$file")"
    echo "==> Applying $name"
    mysql -u "$SINGLESTORE_USER" -h "$SINGLESTORE_HOST" -P "$SINGLESTORE_PORT" \
        --default-auth=mysql_native_password \
        --password="$SINGLESTORE_PASSWORD" \
        < "$file"
done

echo "All migrations applied."
