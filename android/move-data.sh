#!/usr/bin/env bash
# Copies the PC cleaner's database (labels, rules, saved offers, run history)
# into the phone app over adb. Needs the debug build, which allows run-as.
#
#   ./move-data.sh [--session] [DATA_DIR]
#
# --session also moves the pairing (session.json), so the phone app needs no
# new pairing. Stop the PC cleaner first: two cleaners can't share one session.
# DATA_DIR defaults to the repository's data/ folder.
set -euo pipefail

PKG=io.github.ahbanavi.smscleaner
session=0
if [ "${1:-}" = "--session" ]; then session=1; shift; fi
DATA="${1:-$(cd "$(dirname "$0")/.." && pwd)/data}"
ADB="${ADB:-adb}"

[ -f "$DATA/sms-cleaner.db" ] || { echo "no database in $DATA" >&2; exit 1; }
"$ADB" shell pm path "$PKG" >/dev/null || { echo "install the app first" >&2; exit 1; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
# A consistent copy even while the PC cleaner is running.
python3 -I -c 'import sqlite3, sys
src = sqlite3.connect("file:" + sys.argv[1] + "?mode=ro", uri=True)
dst = sqlite3.connect(sys.argv[2])
src.backup(dst)
dst.close()' "$DATA/sms-cleaner.db" "$tmp/sms-cleaner.db"

in_app() { "$ADB" shell "run-as $PKG sh -c '$1'"; }

"$ADB" shell am force-stop "$PKG"
in_app 'mkdir -p files/data && rm -f files/data/sms-cleaner.db-wal files/data/sms-cleaner.db-shm'
in_app 'cat > files/data/sms-cleaner.db' < "$tmp/sms-cleaner.db"
if [ "$session" = 1 ]; then
  [ -f "$DATA/session.json" ] || { echo "no session.json in $DATA" >&2; exit 1; }
  in_app 'cat > files/data/session.json && chmod 600 files/data/session.json' < "$DATA/session.json"
fi

want=$(stat -c %s "$tmp/sms-cleaner.db")
got=$(in_app 'stat -c %s files/data/sms-cleaner.db' | tr -d '\r')
[ "$want" = "$got" ] || { echo "copy failed: $got of $want bytes arrived" >&2; exit 1; }
echo "moved the database ($got bytes)$([ "$session" = 1 ] && echo ' and the pairing')"
