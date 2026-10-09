#!/usr/bin/env bash
# The node test: installs node/ into an empty node folder, starts the stack
# with titan-backend's images built from source (decision #149) and a
# self-signed certificate (decision #82), checks it over HTTPS, then removes
# the stack, its volume and the images it built; the files it wrote into the
# node folder stay.
# Usage: scripts/node-test.sh <empty node folder> <titan-backend checkout>
set -euo pipefail

if [[ $# -ne 2 ]]; then
    echo "usage: $0 <empty node folder> <titan-backend checkout>" >&2
    exit 2
fi
mkdir -p "$1"
node=$(realpath "$1")
backend=$(realpath "$2")
# A folder that holds anything may be a real node; the test never touches one.
if [[ -n $(ls -A "$node") ]]; then
    echo "$0: the node folder $node is not empty; give the node test an empty one" >&2
    exit 2
fi
repository=$(dirname "$(dirname "$(realpath "$0")")")
api_image=titan-api:node-test
admin_image=titan-admin:node-test
work=$(mktemp -d)

# The project name differs from a real node's, `titan`, so the test never
# touches a real node's containers or volume.
compose() { docker compose --project-name titan-node-test --file "$node/compose.yaml" "$@"; }
fail() {
    echo "FAIL: $*" >&2
    exit 1
}

cleanup() {
    status=$?
    if [[ -f $node/compose.yaml ]]; then
        if [[ $status -ne 0 ]]; then compose logs --no-color || true; fi
    fi
    # Every container of the project first, one-off ones such as the
    # placeholder included, so the network is free when `down` removes it.
    docker ps --all --quiet --filter label=com.docker.compose.project=titan-node-test |
        xargs --no-run-if-empty docker rm --force || true
    if [[ -f $node/compose.yaml ]]; then
        compose down --volumes || true
    fi
    docker image rm "$api_image" "$admin_image" || true
    rm -rf "$work"
}
trap cleanup EXIT

echo "== Building the backend's images"
docker build --target api --tag "$api_image" "$backend"
docker build --target admin --tag "$admin_image" "$backend"

echo "== Installing the node into $node"
cp -R "$repository/node/." "$node/"
printf 'TITAN_API_IMAGE=%s\nTITAN_ADMIN_IMAGE=%s\n' "$api_image" "$admin_image" >"$node/.env"
# NOTE: secrets and the TLS key are 0644 in 0700 folders, because the
# containers read them as different users; decision #77 asks for 0600. The
# controller's secret store, a later step, gives each file to its reader.
install -d -m 0700 "$node/secrets" "$node/tls"
openssl rand -hex 32 >"$node/secrets/db_password"
: >"$node/secrets/claude_token"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -noenc \
    -days 1 -subj /CN=localhost -addext subjectAltName=DNS:localhost \
    -keyout "$node/tls/key.pem" -out "$node/tls/cert.pem"
chmod 0644 "$node/secrets/db_password" "$node/secrets/claude_token" "$node/tls/key.pem" "$node/tls/cert.pem"

echo "== Starting the stack"
compose up --wait --wait-timeout 300

echo "== Creating the owner"
owner=owner
password=$(openssl rand -hex 16)
printf '%s\n%s\n%s\n' "$owner" "$password" "$password" |
    compose run --rm -T migrate titan-admin create-owner

echo "== Checking the stack"
# request <name> <curl arguments>: saves the headers and body of one request
# under $work/<name> and prints the status code and content type.
request() {
    local name=$1
    shift
    curl --silent --show-error --cacert "$node/tls/cert.pem" \
        --dump-header "$work/$name.headers" --output "$work/$name.body" \
        --write-out '%{http_code} %{content_type}' "$@"
}

result=$(request anonymous https://localhost/api/v1/me)
[[ $result == "401 application/problem+json" ]] ||
    fail "GET /api/v1/me without a session: expected 401 application/problem+json, got $result"
echo "ok: /api/v1/me without a session answers 401 application/problem+json"

# Credentials go through files in $work, never the command line, where
# every user of the machine could read them.
printf '{"username": "%s", "password": "%s", "name": "node test"}' "$owner" "$password" >"$work/sign-in.json"
result=$(request sign-in https://localhost/api/v1/devices \
    --header 'Content-Type: application/json' --data "@$work/sign-in.json")
[[ $result == "201 application/json" ]] ||
    fail "POST /api/v1/devices (sign in): expected 201 application/json, got $result"
python3 -c 'import json, sys; print("Authorization: Bearer " + json.load(sys.stdin)["token"])' \
    <"$work/sign-in.body" >"$work/authorization"
echo "ok: signing in answers 201 with a device token"

result=$(request whoami https://localhost/api/v1/me --header "@$work/authorization")
[[ $result == "200 application/json" ]] ||
    fail "GET /api/v1/me signed in: expected 200 application/json, got $result"
username=$(python3 -c 'import json, sys; print(json.load(sys.stdin)["username"])' <"$work/whoami.body")
[[ $username == "$owner" ]] || fail "GET /api/v1/me signed in: expected username $owner, got $username"
echo "ok: /api/v1/me signed in answers the owner's username"

result=$(request root https://localhost/)
[[ $result == 404* ]] || fail "GET /: expected 404, got $result"
echo "ok: / answers 404"

version=$(curl --silent --http2 --cacert "$node/tls/cert.pem" --output /dev/null --write-out '%{http_version}' https://localhost/)
[[ $version == 2 ]] || fail "expected HTTP/2, got HTTP/$version"
echo "ok: nginx speaks HTTP/2"

security_headers=(
    "strict-transport-security: max-age=63072000"
    "content-security-policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; require-trusted-types-for 'script'"
    "x-content-type-options: nosniff"
    "referrer-policy: no-referrer"
    "x-frame-options: DENY"
    "cross-origin-opener-policy: same-origin"
    "cross-origin-resource-policy: same-origin"
    "permissions-policy: camera=(), microphone=(), geolocation=(), payment=(), usb=()"
    "server: nginx"
)
for name in anonymous whoami root; do
    for header in "${security_headers[@]}"; do
        tr -d '\r' <"$work/$name.headers" | grep --quiet --ignore-case --line-regexp --fixed-strings -- "$header" ||
            fail "response '$name' lacks the header '$header'"
    done
done
echo "ok: every security header is on /api responses and on the / 404"

# tls <version> [<ciphers>]: connects with only that TLS version and prints
# openssl's output; SECLEVEL=0 lets the client offer old versions and
# ciphers, so a refusal comes from nginx and not from the client's defaults.
tls() {
    openssl s_client -connect localhost:443 -servername localhost "-$1" \
        -cipher "${2:-DEFAULT}:@SECLEVEL=0" -CAfile "$node/tls/cert.pem" \
        -verify_return_error </dev/null 2>&1
}
for version in tls1_2 tls1_3; do
    tls "$version" >"$work/$version.log" || fail "$version does not connect: $(tail -n 5 "$work/$version.log")"
done
echo "ok: TLS 1.2 and 1.3 connect"
if tls tls1_1 >"$work/tls1_1.log"; then fail "TLS 1.1 connects"; fi
grep --quiet 'alert protocol version' "$work/tls1_1.log" ||
    fail "TLS 1.1 failed, but not with nginx's refusal: $(tail -n 5 "$work/tls1_1.log")"
echo "ok: nginx refuses TLS 1.1"
# A cipher outside Mozilla's list that the test's EC certificate could use.
if tls tls1_2 ECDHE-ECDSA-AES128-SHA >"$work/weak-cipher.log"; then fail "TLS 1.2 with ECDHE-ECDSA-AES128-SHA connects"; fi
grep --quiet 'alert handshake failure' "$work/weak-cipher.log" ||
    fail "ECDHE-ECDSA-AES128-SHA failed, but not with nginx's refusal: $(tail -n 5 "$work/weak-cipher.log")"
echo "ok: nginx refuses a cipher outside Mozilla's list"

if curl --silent --max-time 5 http://localhost:80/ >/dev/null; then fail "something answers on port 80"; fi
ports=$(docker port "$(compose ps --quiet nginx)")
published=$(grep --invert-match --extended-regexp '^8443/tcp -> (0\.0\.0\.0|\[::\]):443$' <<<"$ports" || true)
[[ -z $published ]] || fail "nginx publishes more than 443: $published"
for service in api db; do
    published=$(docker port "$(compose ps --quiet "$service")")
    [[ -z $published ]] || fail "$service publishes a port on the host: $published"
done
echo "ok: only nginx publishes a port, 443; nothing answers on port 80"

result=$(request probe 'https://localhost/api/v1/me?probe=secret')
[[ $result == 401* ]] || fail "GET /api/v1/me?probe=secret: expected 401, got $result"
logs=$(compose logs --no-color --no-log-prefix nginx)
if grep --quiet --fixed-strings 'probe=secret' <<<"$logs"; then fail "nginx logs the query string"; fi
python3 -c '
import json, re, sys
lines = [json.loads(line) for line in sys.stdin if line.startswith("{")]
probes = [line for line in lines if line["path"] == "/api/v1/me" and line["status"] == 401]
assert probes, "no JSON access log line for /api/v1/me"
assert all(re.fullmatch("[0-9a-f]{32}", line["request_id"]) for line in probes), "no request id"
print("access log line:", json.dumps(probes[-1]))
' <<<"$logs" || fail "nginx's access log is not JSON lines with the request id"
echo "ok: nginx logs JSON lines with the request id and without the query string"

# NOTE: the server-sent events stream through nginx is not checked here: the
# only stream so far, a chat reply, needs Claude. The end-to-end test of
# titan-web in build-plan stage 5 covers it.

echo "== Recreating the api with a new address"
address() { docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1"; }
before=$(address "$(compose ps --quiet api)")
compose rm --stop --force api
# Docker would hand the freed address straight back to the new api; a
# placeholder container takes it first, so the api really moves.
placeholder=$(compose run --detach --no-deps migrate sleep 300)
compose up --detach --force-recreate --wait --wait-timeout 300 api
after=$(address "$(compose ps --quiet api)")
docker rm --force "$placeholder" >/dev/null
echo "api address before: $before, after: $after"
[[ $before != "$after" ]] || fail "the recreated api kept its address $before, so nothing was checked"
# nginx resolves the name again within 10 seconds (valid=10s); until then
# it may answer 502. Ask about once a second for up to 30 seconds.
for _ in {1..30}; do
    result=$(request recreated https://localhost/api/v1/me) || result="no answer"
    [[ $result == "401 application/problem+json" ]] && break
    sleep 1
done
[[ $result == "401 application/problem+json" ]] ||
    fail "GET /api/v1/me after recreating the api: expected 401 application/problem+json within 30 seconds, got $result"
echo "ok: nginx reaches the recreated api"

echo "== The node test passed"
