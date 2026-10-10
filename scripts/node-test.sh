#!/usr/bin/env bash
# The node test: installs node/ into an empty node folder, starts the stack
# with titan-backend's images and the controller's image built from source
# (decision #149) and a self-signed certificate (decision #82), checks it over
# HTTPS and through the controller's socket, then removes the stack, its
# volumes and the images it built; the files it wrote into the node folder
# stay.
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
# The controller reads the node folder as its own user, not the folder's owner.
chmod 0755 "$node"
repository=$(dirname "$(dirname "$(realpath "$0")")")
api_image=titan-api:node-test
admin_image=titan-admin:node-test
controller_image=titan-controller:node-test
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
    docker image rm "$api_image" "$admin_image" "$controller_image" || true
    rm -rf "$work"
}
trap cleanup EXIT

echo "== Building the backend's images"
docker build --target api --tag "$api_image" "$backend"
docker build --target admin --tag "$admin_image" "$backend"
echo "== Building the controller's image"
docker build --tag "$controller_image" "$repository"

echo "== Installing the node into $node"
cp -R "$repository/node/." "$node/"
cat >"$node/.env" <<EOF
TITAN_API_IMAGE=$api_image
TITAN_ADMIN_IMAGE=$admin_image
TITAN_CONTROLLER_IMAGE=$controller_image
TITAN_NODE_DIR=$node
TITAN_COMPOSE_PROJECT=titan-node-test
DOCKER_GID=$(stat -c %g /var/run/docker.sock)
EOF
# The secret store belongs to the controller's user, mode 0700 (decision
# #163). Making a folder another user's needs root, which the controller's
# image has through Docker. The Claude token stays empty: no test calls
# Claude.
docker run --rm --user 0:0 --entrypoint sh --volume "$node:$node" "$controller_image" -c '
install -d -o 10002 -g 10002 -m 0700 "$1/secrets" &&
install -o 10002 -g 10002 -m 0644 /dev/null "$1/secrets/claude_token"' sh "$node"
compose run --rm --no-deps controller secrets init >/dev/null ||
    fail "titan-controller secrets init failed"
# The secret files and the TLS key are 0644 in 0700 folders: compose mounts a
# file with its owner and mode, and the containers read them as different
# users (decision #77).
install -d -m 0700 "$node/tls"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -noenc \
    -days 1 -subj /CN=localhost -addext subjectAltName=DNS:localhost \
    -keyout "$node/tls/key.pem" -out "$node/tls/cert.pem"
chmod 0644 "$node/tls/key.pem" "$node/tls/cert.pem"

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

# The password-guessing limit (decision #27): 10 wrong passwords from one
# address refuse that address and account, even with the right password.
# One request a second, nginx's rate, so nginx's own limit never answers.
printf '{"username": "%s", "password": "wrong", "name": "node test"}' "$owner" >"$work/wrong.json"
for attempt in {1..10}; do
    result=$(request wrong https://localhost/api/v1/devices \
        --header 'Content-Type: application/json' --data "@$work/wrong.json")
    [[ $result == "401 application/problem+json" ]] ||
        fail "wrong password, attempt $attempt: expected 401 application/problem+json, got $result"
    sleep 1
done
result=$(request locked https://localhost/api/v1/devices \
    --header 'Content-Type: application/json' --data "@$work/sign-in.json")
[[ $result == "429 application/problem+json" ]] ||
    fail "the right password after 10 wrong ones: expected the api's 429 application/problem+json, got $result"
tr -d '\r' <"$work/locked.headers" | grep --quiet --ignore-case --line-regexp --extended-regexp 'retry-after: [0-9]+' ||
    fail "the api's 429 lacks a Retry-After header"
echo "ok: after 10 wrong passwords the right one answers 429 with Retry-After"

# The limit counts the client's address, not nginx's. Seen from the node's
# network, the test's requests come from its gateway, 172.31.250.1.
# from_nginx <address>: signs the owner in at the api directly from nginx,
# the trusted proxy, with X-Forwarded-For naming <address>, and prints the
# status code.
from_nginx() {
    docker exec --interactive "$(compose ps --quiet nginx)" curl --silent --show-error \
        --output /dev/null --write-out '%{http_code}' --header 'Content-Type: application/json' \
        --header "X-Forwarded-For: $1" --data @- http://api:8000/api/v1/devices <"$work/sign-in.json"
}
# From nginx the header is believed: another address signs in, while the
# test's own address is locked. Were the header ignored, every request from
# nginx would count as nginx's own address, which the wrong passwords above
# locked, and 192.0.2.1 would answer 429 too.
result=$(from_nginx 192.0.2.1) || result="no answer"
[[ $result == 201 ]] ||
    fail "signing in from nginx with X-Forwarded-For 192.0.2.1: expected 201, got $result; the api does not believe nginx's X-Forwarded-For (TITAN_TRUSTED_PROXIES)"
result=$(from_nginx 172.31.250.1) || result="no answer"
[[ $result == 429 ]] ||
    fail "signing in from nginx with X-Forwarded-For 172.31.250.1: expected 429, got $result; the test's address is not 172.31.250.1"
# From any other container the header is ignored, and its own address is not locked.
result=$(compose run --rm --no-deps -T migrate python3 -c '
import sys, urllib.error, urllib.request
request = urllib.request.Request(
    "http://api:8000/api/v1/devices",
    data=sys.stdin.buffer.read(),
    headers={"Content-Type": "application/json", "X-Forwarded-For": "172.31.250.1"},
)
try:
    print(urllib.request.urlopen(request, timeout=10).status)
except urllib.error.HTTPError as error:
    print(error.code)
' <"$work/sign-in.json") || result="no answer"
[[ $result == 201 ]] ||
    fail "signing in from another container with X-Forwarded-For 172.31.250.1: expected 201, got $result; the api believes a container other than nginx"
echo "ok: the api believes X-Forwarded-For from nginx only"

# nginx's own limit: a burst of sign-ins from one address meets its 429,
# problem+json like every error of the API. An empty body fails fast in
# the api, before any password check.
for _ in {1..30}; do
    result=$(request flooded https://localhost/api/v1/devices \
        --header 'Content-Type: application/json' --data '{}')
    [[ $result == 429* ]] && break
done
[[ $result == "429 application/problem+json" ]] ||
    fail "30 rapid sign-ins: expected nginx's 429 application/problem+json at some point, got $result last"
echo "ok: nginx answers 429 application/problem+json to a burst of sign-ins"

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
for name in anonymous whoami locked flooded root; do
    for header in "${security_headers[@]}"; do
        tr -d '\r' <"$work/$name.headers" | grep --quiet --ignore-case --line-regexp --fixed-strings -- "$header" ||
            fail "response '$name' lacks the header '$header'"
    done
done
echo "ok: every security header is on /api responses, both 429s and the / 404"

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
for service in api db controller; do
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

echo "== Checking the controller"
# The controller serves only its socket (decision #78; node.md, "Node
# controller", criteria 3 and 4): no network at all, and a socket only its
# own user and the api's group can use, in a volume only the api shares.
network=$(docker inspect --format '{{.HostConfig.NetworkMode}}' "$(compose ps --quiet controller)")
[[ $network == none ]] ||
    fail "the controller has the network $network, want none (node.md, Node controller, criterion 3)"
socket=$(compose exec -T controller stat -c '%a %u %g' /run/titan-controller/controller.sock) || socket="no socket"
[[ $socket == "660 10002 10001" ]] ||
    fail "the controller's socket: expected mode 660, owner 10002, group 10001, got $socket (node.md, Node controller, criterion 4)"
folder=$(compose exec -T controller stat -c '%a %u %g' /run/titan-controller)
[[ $folder == "750 10002 10001" ]] ||
    fail "the socket's folder: expected mode 750, owner 10002, group 10001, got $folder (node.md, Node controller, criterion 4)"
store=$(compose exec -T controller stat -c '%a %u' "$node/secrets" "$node/secrets/db_password" | tr '\n' ' ')
[[ $store == "700 10002 644 10002 " ]] ||
    fail "the secret store and db_password: expected 700 10002 and 644 10002, got $store (node.md, secrets storage, criteria 2 and 4)"
echo "ok: secrets init created db_password in the controller's 0700 secret store"
for service in nginx db migrate; do
    mounts=$(docker inspect --format '{{range .Mounts}}{{.Name}} {{end}}' "$(compose ps --all --quiet "$service")")
    [[ $mounts != *controller-socket* ]] ||
        fail "$service mounts the controller's socket volume; only the api may (node.md, Node controller, criterion 4)"
done
# Another user in the api's own container is refused by the socket's
# folder and mode.
result=$(compose exec -T --user 10003:10003 api python -c '
import socket
try:
    socket.socket(socket.AF_UNIX).connect("/run/titan-controller/controller.sock")
    print("connected")
except PermissionError:
    print("refused")
') || result="no answer"
[[ $result == refused ]] ||
    fail "user 10003 in the api's container connecting to the controller's socket: expected refused, got $result (node.md, Node controller, criterion 4)"
echo "ok: the controller has no network; its socket is 0660, user 10002, group 10001, in a 0750 folder in a volume only the api mounts; another user is refused (node.md, Node controller, criteria 3 and 4)"

# Docker access works from inside the controller, apart from its own code.
compose exec -T controller docker compose --project-directory "$node" --project-name titan-node-test \
    ps --all --format json >"$work/ps.json" || fail "docker compose ps does not run inside the controller"
services=$(python3 -c 'import json, sys; print(" ".join(sorted(json.loads(line)["Service"] for line in sys.stdin)))' <"$work/ps.json")
[[ $services == "api controller db migrate nginx" ]] ||
    fail "docker compose ps inside the controller: expected api controller db migrate nginx, got $services"
echo "ok: docker compose ps runs inside the controller"

# GET /health over the socket, from the api, the only container that shares it.
# Prints the services as JSON, {"name": "status"}, or fails.
controller_health() {
    compose exec -T api python -c '
import http.client, socket

class Connection(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(30)
        self.sock.connect("/run/titan-controller/controller.sock")

connection = Connection("controller")
connection.request("GET", "/health")
response = connection.getresponse()
print(response.status, response.getheader("Content-Type"))
print(response.read().decode())
' >"$work/health" || fail "GET /health over the controller's socket: no answer"
    python3 -c '
import json, sys
status = sys.stdin.readline().strip()
body = sys.stdin.read()
assert status == "200 application/json", f"expected 200 application/json, got {status}: {body}"
print(json.dumps({service["name"]: service["status"] for service in json.loads(body)["services"]}, sort_keys=True))
' <"$work/health" || fail "GET /health over the controller's socket: $(cat "$work/health")"
}

# want_health <expected services as JSON> <what the check shows>
want_health() {
    local got
    got=$(controller_health)
    [[ $got == "$1" ]] || fail "GET /health over the controller's socket $2: expected $1, got $got"
    echo "ok: GET /health over the controller's socket $2"
}

want_health '{"api": "healthy", "controller": "running", "db": "healthy", "migrate": "done", "nginx": "healthy"}' \
    "answers every service's status (node.md, Node controller, criterion 3)"

# The owner sees the same through the API: nginx, the api, the socket and the
# controller (decision #161).
result=$(request node-health https://localhost/api/v1/node/health --header "@$work/authorization")
[[ $result == "200 application/json" ]] ||
    fail "GET /api/v1/node/health as the owner: expected 200 application/json, got $result: $(cat "$work/node-health.body")"
got=$(python3 -c '
import json, sys
print(json.dumps({s["name"]: s["status"] for s in json.load(sys.stdin)["services"]}, sort_keys=True))
' <"$work/node-health.body")
want='{"api": "healthy", "controller": "running", "db": "healthy", "migrate": "done", "nginx": "healthy"}'
[[ $got == "$want" ]] || fail "GET /api/v1/node/health as the owner: expected $want, got $got"
echo "ok: GET /api/v1/node/health answers the owner every service's status (node.md, Node controller, criterion 1)"

# Without the controller the api answers 503 with its own problem type
# (decision #159).
compose stop controller >/dev/null 2>&1
result=$(request node-health-down https://localhost/api/v1/node/health --header "@$work/authorization")
type=$(python3 -c 'import json, sys; print(json.load(sys.stdin).get("type"))' <"$work/node-health-down.body")
[[ "$result $type" == "503 application/problem+json urn:titan:problem:controller-unavailable" ]] ||
    fail "GET /api/v1/node/health without the controller: expected 503 controller-unavailable, got $result $type"
echo "ok: GET /api/v1/node/health without the controller answers 503 controller-unavailable"
compose start controller >/dev/null 2>&1
# The controller needs a moment to create its socket again.
for _ in {1..10}; do controller_health >/dev/null 2>&1 && break; sleep 1; done

# nginx is the last check's to break: nothing below goes through it.
compose stop nginx >/dev/null 2>&1
want_health '{"api": "healthy", "controller": "running", "db": "healthy", "migrate": "done", "nginx": "down"}' \
    "shows a stopped service as down, not done"
compose rm --force nginx >/dev/null 2>&1
want_health '{"api": "healthy", "controller": "running", "db": "healthy", "migrate": "done", "nginx": "down"}' \
    "shows a service without a container as down"

echo "== The node test passed"
