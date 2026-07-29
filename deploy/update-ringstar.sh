#!/bin/bash
set -e
BRANCH="${1:-main}"
cd /opt/ringstar-src
git fetch --depth 50 origin "$BRANCH"
git checkout -f -B "$BRANCH" FETCH_HEAD
git reset --hard FETCH_HEAD
echo "COMMIT=$(git log -1 --oneline)"
test -f frontend/src/components/common/GalaxyBackground.vue && echo galaxy_ok
echo BUILD_START
docker build -t ringstar:latest -f deploy/Dockerfile .
echo BUILD_DONE
cd /opt/sub2api
docker compose up -d --force-recreate --no-deps sub2api
sleep 15
docker compose ps
curl -sS http://127.0.0.1:8080/ | head -c 400
echo
curl -sS -o /dev/null -w "home:%{http_code}\n" http://127.0.0.1:8080/home
curl -sS -o /dev/null -w "login:%{http_code}\n" http://127.0.0.1:8080/login