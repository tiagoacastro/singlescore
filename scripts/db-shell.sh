#!/usr/bin/env bash
# Open an interactive mysql shell against the configured SingleStore cluster.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="$repo_root/.env"

# shellcheck disable=SC1090
set -a; . "$env_file"; set +a

exec mysql -u "$SINGLESTORE_USER" -h "$SINGLESTORE_HOST" -P "$SINGLESTORE_PORT" \
    --default-auth=mysql_native_password \
    --password="$SINGLESTORE_PASSWORD" \
    "${SINGLESTORE_DATABASE:-singlescore}"
