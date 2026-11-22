#!/usr/bin/env bash
# Run the normal integration target with an isolated Unix-socket publication DB.
set -euo pipefail
bin=$(pg_config --bindir)
root=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/p.XXXXXX")
cleanup() {
  local status=$?
  if [[ -f "$root/data/postmaster.pid" ]]; then
    "$bin/pg_ctl" -D "$root/data" -m fast -w stop || exit 1
  fi
  rm -rf -- "$root" || exit 1
  exit "$status"
}
trap cleanup EXIT
export LC_ALL=C
"$bin/initdb" -D "$root/data" -U publish_test --auth-local=trust --auth-host=reject --no-locale -E UTF8 >/dev/null
"$bin/pg_ctl" -D "$root/data" -l "$root/server.log" -o "-k '$root' -p 5432 -c listen_addresses=''" -w start || { cat "$root/server.log" >&2; exit 1; }
"$bin/createdb" -h "$root" -p 5432 -U publish_test price_publish_test
unset PRICE_PUBLISH_TEST_SCHEMA PRICE_PUBLISH_TEST_REV PRICE_PUBLISH_TEST_FOLLOW
export PRICE_PUBLISH_TEST_SOCKET="$root"
"$@"