#!/bin/bash
# deploy.sh — vet, test, build and deploy nicelink to Fly.io after source
# changes. Run from anywhere; it operates on the repo it lives in.
#
# Database safety: production data lives in the nicelink_data volume mounted
# at /data (fly.toml [mounts]). `fly deploy` never modifies volumes, and this
# script refuses to run unless fly.toml still deploys app possdog-nicelink
# WITH that volume mounted — so a deploy can never boot a machine on empty
# ephemeral storage. After deploying it verifies the volume is attached and
# still holds link_shortener.bolt + secret.key. It never destroys apps,
# machines or volumes.
set -euo pipefail

cd "$(dirname "$0")"

APP=possdog-nicelink
VOLUME=nicelink_data
MOUNT_DEST=/data

for tool in fly jq go curl; do
	command -v "$tool" >/dev/null || { echo "error: $tool is required" >&2; exit 1; }
done

echo "==> go vet + tests"
go vet ./...
go test -count=1 ./...

echo "==> config guards (protect the production database)"
fly config validate
grep -qE "^app = \"$APP\"" fly.toml || { echo "error: fly.toml app != $APP — refusing to deploy the wrong app" >&2; exit 1; }
grep -qE '^\[mounts\]' fly.toml || { echo "error: fly.toml has no [mounts] — refusing to deploy without the volume" >&2; exit 1; }
grep -qE "source = \"$VOLUME\"" fly.toml || { echo "error: fly.toml [mounts] source != $VOLUME" >&2; exit 1; }
grep -qE "destination = \"$MOUNT_DEST\"" fly.toml || { echo "error: fly.toml [mounts] destination != $MOUNT_DEST" >&2; exit 1; }
fly volumes list -a "$APP" --json | jq -e --arg v "$VOLUME" 'any(.[]; .name == $v and .state == "created")' >/dev/null \
	|| { echo "error: volume $VOLUME not found on $APP — create it first" >&2; exit 1; }
echo "    app=$APP, volume=$VOLUME mounted at $MOUNT_DEST — OK"

echo "==> fly deploy --remote-only (up to 3 attempts; remote-builder 503s are transient)"
for attempt in 1 2 3; do
	if fly deploy --remote-only; then break; fi
	if [ "$attempt" -eq 3 ]; then echo "error: deploy failed after 3 attempts" >&2; exit 1; fi
	echo "    attempt $attempt failed — retrying in 15s" >&2
	sleep 15
done

echo "==> post-deploy verification"
# With auto_stop_machines and min_machines_count = 0 the machine may be left
# stopped/suspended by the deploy, so wake it with a real request first —
# autostart boots it and proves the edge-to-app path end to end.
for i in $(seq 1 24); do
	curl -sf -o /dev/null "https://$APP.fly.dev/" && break
	sleep 5
done
curl -sf -o /dev/null "https://$APP.fly.dev/" || { echo "error: https://$APP.fly.dev/ not responding (autostart wake failed)" >&2; exit 1; }
echo "    https://$APP.fly.dev/ responds 200 (machine woke from autostop)"

# The app machine must be started with at least one passing health check.
MACHINE_ID=""
for i in $(seq 1 18); do
	STATUS_JSON=$(fly status -a "$APP" --json)
	MACHINE_ID=$(echo "$STATUS_JSON" | jq -r '[.Machines[] | select(.state == "started" and .config.env.FLY_PROCESS_GROUP == "app") | .id][0]')
	if [ -n "$MACHINE_ID" ] && [ "$MACHINE_ID" != "null" ]; then
		PASSING=$(echo "$STATUS_JSON" | jq -r --arg id "$MACHINE_ID" '[.Machines[] | select(.id == $id) | .checks[]? | select(.status == "passing")] | length')
		[ "$PASSING" -gt 0 ] && break
	fi
	sleep 10
done
[ -n "$MACHINE_ID" ] && [ "$MACHINE_ID" != "null" ] || { echo "error: no started app machine after deploy" >&2; exit 1; }
echo "    machine $MACHINE_ID started, $PASSING health check(s) passing"

# The volume must be attached to that machine and actually mounted at /data
# (files existing is not enough — on a boot that raced the volume attach,
# /data is the ephemeral container rootfs and any writes there are lost on
# the next restart).
fly volumes list -a "$APP" --json | jq -e --arg v "$VOLUME" --arg m "$MACHINE_ID" 'any(.[]; .name == $v and .attached_machine_id == $m)' >/dev/null \
	|| { echo "error: volume $VOLUME not attached to $MACHINE_ID — database NOT mounted" >&2; exit 1; }
fly ssh console -C "mount" -a "$APP" | grep -q " on /data " \
	|| { echo "error: /data is not a mounted volume on $MACHINE_ID — database would be ephemeral" >&2; exit 1; }
LS_OUT=$(fly ssh console -C "ls -l /data" -a "$APP")
echo "$LS_OUT" | grep -q link_shortener.bolt || { echo "error: /data/link_shortener.bolt missing — database not intact" >&2; exit 1; }
echo "$LS_OUT" | grep -q secret.key || { echo "error: /data/secret.key missing — secret not intact" >&2; exit 1; }
echo "    volume attached to $MACHINE_ID; /data contents:"
echo "$LS_OUT" | sed 's/^/      /'

echo "==> deployed; production database on volume $VOLUME untouched"
