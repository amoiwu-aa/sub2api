#!/bin/bash
set -euo pipefail

BRANCH="${1:-main}"
SOURCE_DIR="${RINGSTAR_SOURCE_DIR:-/opt/ringstar-src}"
DEPLOY_DIR="${RINGSTAR_DEPLOY_DIR:-/opt/sub2api}"
RUN_CURSOR_E2E="${RUN_CURSOR_E2E:-1}"
BUILD_DIR=""
OVERRIDE_FILE="$DEPLOY_DIR/.ringstar-image.override.yml"
ROLLBACK_TAG=""

cleanup() {
  if [ -n "$BUILD_DIR" ] && [ -d "$BUILD_DIR" ]; then
    git -C "$SOURCE_DIR" worktree remove --force "$BUILD_DIR" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

cd "$SOURCE_DIR"
if ! git diff --quiet || ! git diff --cached --quiet || [ -n "$(git ls-files --others --exclude-standard)" ]; then
  echo "FATAL: $SOURCE_DIR has uncommitted changes; refusing to discard or deploy them"
  exit 1
fi

git fetch --depth 50 origin "$BRANCH"
TARGET_COMMIT="$(git rev-parse FETCH_HEAD)"
SHORT_COMMIT="$(git rev-parse --short=12 "$TARGET_COMMIT")"
IMAGE_TAG="ringstar:${SHORT_COMMIT}"
BUILD_DIR="/tmp/ringstar-build-${SHORT_COMMIT}-$$"

git worktree add --detach "$BUILD_DIR" "$TARGET_COMMIT"
echo "COMMIT=$(git -C "$BUILD_DIR" log -1 --oneline)"
test -f "$BUILD_DIR/frontend/src/components/common/GalaxyBackground.vue" && echo galaxy_ok

echo "BUILD_START image=$IMAGE_TAG"
docker build -t "$IMAGE_TAG" -f "$BUILD_DIR/deploy/Dockerfile" "$BUILD_DIR"
echo "BUILD_DONE image=$IMAGE_TAG"

cd "$DEPLOY_DIR"
CURRENT_IMAGE_ID="$(docker inspect sub2api --format '{{.Image}}' 2>/dev/null || true)"
if [ -n "$CURRENT_IMAGE_ID" ]; then
  ROLLBACK_TAG="ringstar:rollback-$(date +%Y%m%d-%H%M%S)"
  docker tag "$CURRENT_IMAGE_ID" "$ROLLBACK_TAG"
  echo "ROLLBACK_IMAGE=$ROLLBACK_TAG"
fi

write_override() {
  local image="$1"
  cat >"$OVERRIDE_FILE" <<EOF
services:
  sub2api:
    image: ${image}
EOF
}

rollback() {
  if [ -z "$ROLLBACK_TAG" ]; then
    echo "ROLLBACK_SKIPPED: no previous image"
    return
  fi
  echo "ROLLBACK_START image=$ROLLBACK_TAG"
  write_override "$ROLLBACK_TAG"
  docker compose -f docker-compose.yml -f "$OVERRIDE_FILE" up -d --force-recreate --no-deps sub2api
  echo "ROLLBACK_DONE"
}

write_override "$IMAGE_TAG"
if ! docker compose -f docker-compose.yml -f "$OVERRIDE_FILE" up -d --force-recreate --no-deps sub2api; then
  rollback
  exit 1
fi

healthy=0
for _ in $(seq 1 30); do
  if curl -fsS --max-time 5 http://127.0.0.1:8080/health >/dev/null; then
    healthy=1
    break
  fi
  sleep 2
done
if [ "$healthy" != "1" ]; then
  echo "FATAL: health check failed"
  docker compose -f docker-compose.yml -f "$OVERRIDE_FILE" logs --tail=200 sub2api || true
  rollback
  exit 1
fi

docker compose -f docker-compose.yml -f "$OVERRIDE_FILE" ps
curl -fsS --max-time 10 http://127.0.0.1:8080/ -o /tmp/ringstar-home-smoke.html
head -c 400 /tmp/ringstar-home-smoke.html
echo
curl -fsS -o /dev/null -w "home:%{http_code}\n" --max-time 10 http://127.0.0.1:8080/home
curl -fsS -o /dev/null -w "login:%{http_code}\n" --max-time 10 http://127.0.0.1:8080/login

if [ "$RUN_CURSOR_E2E" = "1" ]; then
  if ! bash "$BUILD_DIR/deploy/tests/cursor-tool-calling-e2e.sh"; then
    echo "FATAL: Cursor E2E failed"
    rollback
    exit 1
  fi
fi

echo "DEPLOY_OK image=$IMAGE_TAG commit=$TARGET_COMMIT"
