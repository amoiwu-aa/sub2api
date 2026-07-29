#!/bin/bash
set -e
JS=$(curl -sS http://127.0.0.1:8080/ | sed -n 's/.*src="\(\/assets\/index-[^"]*\.js\)".*/\1/p' | head -1)
echo "JS=$JS"
curl -sS "http://127.0.0.1:8080$JS" | grep -c 'tech-bg' || true
curl -sS "http://127.0.0.1:8080$JS" | grep -c 'TechMesh' || true
curl -sS "http://127.0.0.1:8080$JS" | grep -o 'tech-bg__[a-z]*' | sort -u | head -20
curl -sS http://127.0.0.1:8080/api/v1/settings/public | python3 - <<'PY'
import sys,json
d=json.load(sys.stdin)["data"]
print("home_content=", repr(d.get("home_content",""))[:120])
print("site_name=", d.get("site_name"))
PY
