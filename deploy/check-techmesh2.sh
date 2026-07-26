#!/bin/bash
set -e
HTML=$(curl -sS http://127.0.0.1:8080/)
echo "$HTML" | head -c 700
echo
echo '====ASSETS===='
curl -sS http://127.0.0.1:8080/assets/ | head -c 200 || true
# list asset names referenced or on disk via container
docker exec sub2api sh -c 'ls /app/internal/web/dist/assets 2>/dev/null | head -50 || ls /app/web/dist/assets 2>/dev/null | head -50 || find /app -name "*.js" | head -40'
echo '====GREP===='
docker exec sub2api sh -c 'grep -rl "tech-bg\|TechMesh\|tech mesh" /app 2>/dev/null | head -20'
echo '====PUBLIC===='
curl -sS http://127.0.0.1:8080/api/v1/settings/public | head -c 500
echo
