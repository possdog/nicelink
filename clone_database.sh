#!/bin/bash
# clone_database.sh — import an existing link_shortener.bolt + secret.key
# into the persistent volume (/data) of the Fly app possdog-nicelink.
#
# Run this from a directory containing the two files (e.g. an old server).
# See AGENTS.md "Migrating existing data" for why the import works this way:
#   - fly ssh sftp put takes one file at a time and never overwrites, so each
#     file is staged under a fresh .incoming name, then swapped in with mv.
#   - The machine must be running for sftp/console; it is restarted at the
#     end so bbolt re-opens the swapped files (it holds the old inode + lock).
set -euo pipefail

APP=possdog-nicelink

for tool in fly jq; do
	command -v "$tool" >/dev/null || { echo "error: $tool is required" >&2; exit 1; }
done
for f in link_shortener.bolt secret.key; do
	[ -f "$f" ] || { echo "error: $f not found in $(pwd) — run from the directory holding the old database and secret" >&2; exit 1; }
done

# Resolve the running app machine's ID dynamically (we run exactly one).
MACHINE_ID=$(fly status -a "$APP" --json | jq -r '[.Machines[] | select(.state == "started" and .config.env.FLY_PROCESS_GROUP == "app") | .id][0]')
if [ -z "$MACHINE_ID" ] || [ "$MACHINE_ID" = "null" ]; then
	echo "error: no started 'app' machine found for $APP — deploy it first (fly deploy)" >&2
	exit 1
fi
echo "Using machine $MACHINE_ID ($APP)"

fly ssh sftp put link_shortener.bolt /data/link_shortener.bolt.incoming -a "$APP"
fly ssh sftp put secret.key /data/secret.key.incoming -a "$APP"
# NB: -C does not run a shell — one command per call, no && chaining.
fly ssh console -C "mv /data/link_shortener.bolt.incoming /data/link_shortener.bolt" -a "$APP"
fly ssh console -C "mv /data/secret.key.incoming /data/secret.key" -a "$APP"
fly machine restart "$MACHINE_ID" -a "$APP"

echo "Done. Verify an existing alias still redirects, e.g.:"
echo "  curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' https://$APP.fly.dev/<known-alias>"
