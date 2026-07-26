#!/bin/bash
set -e
cd /opt/sub2api
sed -i 's|image: weishaw/sub2api:latest|image: ringstar:latest|' docker-compose.yml
grep 'image:' docker-compose.yml | head -5
docker compose up -d --force-recreate --no-deps sub2api
sleep 12
docker compose ps
echo '====SETTINGS===='
docker compose exec -T postgres psql -U sub2api -d sub2api -c "SELECT key, value FROM settings WHERE key ILIKE '%site%' OR key ILIKE '%subtitle%' OR key ILIKE '%title%' ORDER BY key;"
echo '====UPDATE===='
docker compose exec -T postgres psql -U sub2api -d sub2api -c "UPDATE settings SET value = '\"RingStar\"' WHERE key = 'site_name';"
docker compose exec -T postgres psql -U sub2api -d sub2api -c "UPDATE settings SET value = '\"环星 AI 网关\"' WHERE key = 'site_subtitle';"
docker compose exec -T postgres psql -U sub2api -d sub2api -c "SELECT key, value FROM settings WHERE key IN ('site_name','site_subtitle');"
docker compose restart sub2api
sleep 8
curl -sS -H 'Host: ai.ringstar.win' http://127.0.0.1/ | head -c 800
echo
curl -sS http://127.0.0.1:8080/api/v1/settings/public 2>/dev/null | head -c 1000 || curl -sS http://127.0.0.1:8080/api/settings/public 2>/dev/null | head -c 1000 || true
echo
