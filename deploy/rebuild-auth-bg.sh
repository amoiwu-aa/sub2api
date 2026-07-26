#!/bin/bash
set -e
cd /opt/ringstar-src
echo BUILD_START
docker build -t ringstar:latest -f deploy/Dockerfile .
echo BUILD_DONE
cd /opt/sub2api
docker compose up -d --force-recreate --no-deps sub2api
sleep 10
docker compose ps
# smoke: login page html loads; check chunk mentions tech-bg via binary strings
docker run --rm --entrypoint sh ringstar:latest -c "strings /app/sub2api | grep -c tech-bg"
curl -sS -o /dev/null -w "home:%{http_code}\n" http://127.0.0.1:8080/home
curl -sS -o /dev/null -w "login:%{http_code}\n" http://127.0.0.1:8080/login
