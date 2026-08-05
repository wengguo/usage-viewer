#!/bin/sh
# =============================================================================
# Sub2API Usage Viewer - Remote Install Script
# =============================================================================
# Deploys the usage viewer on the same Docker host as an already-running
# Sub2API stack, WITHOUT modifying Sub2API's containers, configuration,
# environment, or docker-compose files.
#
# The script:
#   1. Locates the running Sub2API application container.
#   2. Reads its database/redis settings from the container's own config.yaml.
#   3. Attaches the usage-viewer container to the same Docker network.
#   4. Starts the viewer bound to 127.0.0.1:8081 (reverse proxy in front).
#
# Prerequisites on the server:
#   - Docker with the compose plugin or plain `docker run` support.
#   - The usage-viewer image already loaded (or reachable from a registry).
#   - A reverse proxy (nginx/caddy) configured per nginx-usage-viewer.conf.
#
# Image loading examples (run on the server, pick ONE):
#   # a) from a registry
#   docker pull your-registry.example.com/sub2api-usage-viewer:latest
#   # b) transferred from another machine
#   docker load -i sub2api-usage-viewer.tar
#
# Usage:
#   chmod +x remote-install.sh
#   ./remote-install.sh
#   ./remote-install.sh --image sub2api-usage-viewer:latest
#   ./remote-install.sh --port 8081 --host-port 127.0.0.1:8081
# =============================================================================

set -eu

IMAGE="${IMAGE:-sub2api-usage-viewer:latest}"
HOST_PORT="${HOST_PORT:-127.0.0.1:8081}"
CONTAINER_PORT=8081
DATA_VOLUME="${DATA_VOLUME:-usage-viewer-data}"

while [ $# -gt 0 ]; do
  case "$1" in
    --image) IMAGE="$2"; shift 2 ;;
    --host-port) HOST_PORT="$2"; shift 2 ;;
    --data-volume) DATA_VOLUME="$2"; shift 2 ;;
    --help)
      echo "Usage: $0 [--image IMAGE] [--host-port HOST:PORT] [--data-volume VOL]"
      exit 0 ;;
    *) echo "Unknown option: $1"; exit 2 ;;
  esac
done

command -v docker >/dev/null 2>&1 || { echo "ERROR: docker is required"; exit 1; }

echo "==> Locating Sub2API application container..."
# Prefer the compose-style name (sub2api), fall back to the first container
# whose image contains 'sub2api'.
SUB2API_CONTAINER="$(docker ps --format '{{.Names}}\t{{.Image}}' | awk -F'\t' '$1=="sub2api" {print $1; exit}')"
if [ -z "$SUB2API_CONTAINER" ]; then
  SUB2API_CONTAINER="$(docker ps --format '{{.Names}}\t{{.Image}}' | awk -F'\t' '$2 ~ /sub2api/ && $1 !~ /postgres/ && $1 !~ /redis/ {print $1; exit}')"
fi
if [ -z "$SUB2API_CONTAINER" ]; then
  echo "ERROR: cannot find the Sub2API container. List running containers with: docker ps"
  exit 1
fi
echo "    using container: $SUB2API_CONTAINER"

echo "==> Locating the usage-viewer container (skip if already running)..."
EXISTING="$(docker ps -a --format '{{.Names}}' | grep -x usage-viewer || true)"
if [ -n "$EXISTING" ]; then
  echo "    existing usage-viewer container found; removing it"
  docker rm -f usage-viewer >/dev/null
fi

echo "==> Reading Sub2API database/redis settings from config.yaml..."
CONFIG_YAML="$(docker exec "$SUB2API_CONTAINER" cat /app/data/config.yaml 2>/dev/null || true)"
if [ -z "$CONFIG_YAML" ]; then
  echo "ERROR: cannot read /app/data/config.yaml from container $SUB2API_CONTAINER"
  exit 1
fi

extract_yaml_value() {
  # $1 = section, $2 = key. Matches "key: value" at the section's indentation.
  printf '%s\n' "$CONFIG_YAML" | awk -v sec="$1" -v key="$2" '
    $0 ~ "^" sec ":" { in_sec=1; next }
    in_sec && $0 ~ "^[a-zA-Z_]" { in_sec=0 }
    in_sec && $0 ~ "^[[:space:]]+" key ":" {
      line=$0; sub(/^[[:space:]]*[a-zA-Z_]+:[[:space:]]*/, "", line)
      gsub(/^["'"'"']|["'"'"']$/, "", line)
      print line
    }'
}

DB_HOST="$(extract_yaml_value database host)"
DB_PORT="$(extract_yaml_value database port)"
DB_USER="$(extract_yaml_value database user)"
DB_PASSWORD="$(extract_yaml_value database password)"
DB_NAME="$(extract_yaml_value database dbname)"
DB_SSLMODE="$(extract_yaml_value database sslmode)"
REDIS_HOST="$(extract_yaml_value redis host)"
REDIS_PORT="$(extract_yaml_value redis port)"
REDIS_PASSWORD="$(extract_yaml_value redis password)"

[ -z "$DB_HOST" ] && { echo "ERROR: database host not found in config.yaml"; exit 1; }
[ -z "$DB_USER" ] && { echo "ERROR: database user not found in config.yaml"; exit 1; }
[ -z "$DB_NAME" ] && DB_NAME="sub2api"
[ -z "$DB_SSLMODE" ] && DB_SSLMODE="disable"
[ -z "$REDIS_HOST" ] && { echo "WARN: redis host not found; current concurrency will report 0"; }
[ -z "$REDIS_PORT" ] && REDIS_PORT="6379"

echo "    db: $DB_USER@$DB_HOST:$DB_PORT/$DB_NAME (sslmode=$DB_SSLMODE)"
echo "    redis: $REDIS_HOST:$REDIS_PORT"

echo "==> Attaching to the Sub2API Docker network..."
NETWORK="$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$SUB2API_CONTAINER")"
if [ -z "$NETWORK" ]; then
  echo "ERROR: cannot determine the network of container $SUB2API_CONTAINER"
  exit 1
fi
echo "    network: $NETWORK"

echo "==> Ensuring the data volume exists..."
docker volume inspect "$DATA_VOLUME" >/dev/null 2>&1 || docker volume create "$DATA_VOLUME" >/dev/null

echo "==> Starting usage-viewer..."
ENV_ARGS=""
[ -n "$DB_HOST" ] && ENV_ARGS="$ENV_ARGS -e DATABASE_HOST=$DB_HOST"
[ -n "$DB_PORT" ] && ENV_ARGS="$ENV_ARGS -e DATABASE_PORT=$DB_PORT"
[ -n "$DB_USER" ] && ENV_ARGS="$ENV_ARGS -e DATABASE_USER=$DB_USER"
[ -n "$DB_PASSWORD" ] && ENV_ARGS="$ENV_ARGS -e DATABASE_PASSWORD=$DB_PASSWORD"
[ -n "$DB_NAME" ] && ENV_ARGS="$ENV_ARGS -e DATABASE_DBNAME=$DB_NAME"
[ -n "$DB_SSLMODE" ] && ENV_ARGS="$ENV_ARGS -e DATABASE_SSLMODE=$DB_SSLMODE"
[ -n "$REDIS_HOST" ] && ENV_ARGS="$ENV_ARGS -e REDIS_HOST=$REDIS_HOST"
[ -n "$REDIS_PORT" ] && ENV_ARGS="$ENV_ARGS -e REDIS_PORT=$REDIS_PORT"
[ -n "$REDIS_PASSWORD" ] && ENV_ARGS="$ENV_ARGS -e REDIS_PASSWORD=$REDIS_PASSWORD"

# shellcheck disable=SC2086
docker run -d \
  --name usage-viewer \
  --restart unless-stopped \
  --network "$NETWORK" \
  -p "$HOST_PORT:$CONTAINER_PORT" \
  -v "$DATA_VOLUME:/app/data" \
  -e SUB2API_USAGE_VIEWER_DATA_DIR=/app/data \
  -e SUB2API_USAGE_VIEWER_LISTEN_ADDR=0.0.0.0:$CONTAINER_PORT \
  -e SUB2API_USAGE_VIEWER_ACKNOWLEDGE_NON_LOOPBACK=true \
  $ENV_ARGS \
  "$IMAGE"

echo "==> Waiting for readiness..."
for i in $(seq 1 30); do
  if docker logs usage-viewer 2>&1 | grep -q '"event":"ready"'; then
    break
  fi
  sleep 1
done

echo ""
echo "Done. The viewer is running."
echo "  Container:  usage-viewer"
echo "  Listen:     $HOST_PORT (localhost only)"
echo ""
echo "Verify locally:   curl -s $HOST_PORT/readyz"
echo "                  curl -s -X POST $HOST_PORT/api/search -H 'Content-Type: application/json' -d '{\"targetType\":\"key\",\"query\":\"yourkey\"}'"
echo ""
echo "Then configure the reverse proxy (see nginx-usage-viewer.conf)."
