# titan-node

The TITAN node: the controller written in Go, the docker compose file, the nginx configuration, and everything needed to install and update a node on a machine.

Part of TITAN, a self-hosted personal AI assistant, planner and tracker. The
product, architecture and rules shared by every TITAN repository are in the
`shared/` submodule ([titan-shared](shared/README.md)).

## Contents

- **Controller** (Go): service health, image updates by digest and rollback, scheduled database backups, node secrets, certificates, running plugins; later the cluster.
- **compose file**: nginx, api, migrate, PostgreSQL and the controller now; web-ui, worker and embeddings later.
- **nginx configuration**: the single entry, TLS, `/` → UI files, `/api` → API, security headers, SSE without buffering.
- **Install and update scripts** for a clean machine.

## Status

Build-plan stage 4, in progress: the node's compose file, nginx with TLS and
the node test exist. The controller reports the services' health over its
socket. See the [build plan](shared/docs/roadmap/plan.md) and
[the controller](docs/controller.md).

## Layout

`node/` holds exactly what is installed into the node folder, `/opt/titan`
([decision #148](shared/docs/decisions/README.md#register)).

| Path | What it is |
|---|---|
| `node/compose.yaml` | The stack: `nginx`, `api`, `migrate` (applies migrations, then exits) and `db`, on the network `172.31.250.0/24`; nginx has the fixed address `172.31.250.2`, the only one the api believes `X-Forwarded-For` from (`TITAN_TRUSTED_PROXIES`). The `controller` has no network and shares its socket's volume with the api only |
| `node/nginx/titan.conf` | nginx: TLS on 443, `/api/` to the API, `/` answers 404 until the web UI arrives, security headers, sign-in (`/api/v1/devices`) limited to 1 request a second per address, up to 11 at once, answered with problem+json when exceeded |
| `cmd/titan-controller/`, `internal/` | The controller in Go; see [the controller](docs/controller.md) |
| `Dockerfile` | The controller's image |
| `go.mod`, `.golangci.yml` | The Go module, with no dependencies, and the linters' settings |
| `docs/controller.md` | The controller: its socket, its API and its settings |
| `scripts/node-test.sh` | The node test: installs `node/` into a node folder, starts the stack and checks it |
| `.github/workflows/checks.yml` | Checks links and the Go code on every pull request |
| `.github/workflows/node.yml` | Runs the node test on every pull request |

Beside `compose.yaml` the node folder holds what is not in git:

| Path | What it is |
|---|---|
| `.env` | `TITAN_API_IMAGE`, `TITAN_ADMIN_IMAGE` and `TITAN_CONTROLLER_IMAGE`, the images; `TITAN_NODE_DIR`, the node folder's path; `DOCKER_GID`, the group of `/var/run/docker.sock`; optionally `TITAN_COMPOSE_PROJECT`, `titan` unless set |
| `secrets/db_password`, `secrets/claude_token` | The secrets, read through `*_FILE` variables |
| `tls/cert.pem`, `tls/key.pem` | The certificate and its key |

## Run the stack by hand

Until the controller installs a node, the steps are the node test's. It needs
Docker, port 443 and the subnet `172.31.250.0/24` free, and a
`titan-backend` checkout to build the images from, since none are published
yet
([decision #149](shared/docs/decisions/README.md#register)).

1. Build the images:
   `docker build --target api --tag titan-api:local ../titan-backend` and the
   same with `--target admin --tag titan-admin:local`; the controller's with
   `docker build --tag titan-controller:local .`.
2. Copy `node/` into the node folder: `cp -R node/. /opt/titan/`. The folder
   must be readable by the controller's user, `10002`: mode `0755`.
3. In the node folder, write `.env`, one per line:
   `TITAN_API_IMAGE=titan-api:local`, `TITAN_ADMIN_IMAGE=titan-admin:local`,
   `TITAN_CONTROLLER_IMAGE=titan-controller:local`, `TITAN_NODE_DIR=/opt/titan`
   and `DOCKER_GID=` followed by the output of
   `stat -c %g /var/run/docker.sock`.
4. Create `secrets/` and `tls/` with mode `0700`; put a random password in
   `secrets/db_password`, the Claude token (or nothing) in
   `secrets/claude_token`, and the certificate and key in `tls/`. Make the
   four files readable (`0644`): the containers read them as other users.
5. Start the stack: `docker compose up --wait`.
6. Create the owner: `docker compose run --rm migrate titan-admin create-owner`.

The API answers on `https://<host name>/api/`. Stop the stack with
`docker compose down`; `--volumes` also deletes the database.

After 10 failed sign-ins within 15 minutes the api refuses that address and
account for 15 minutes; devices already signed in keep working. To lift it
sooner, restart the api: `docker compose restart api`. The count lives in
its memory, so this clears every lock, and chats in progress are cut
([decision #153](shared/docs/decisions/README.md#register)).

## Client addresses behind Tailscale

The api limits password guessing per client address and account, and takes
the address from nginx only. On a node reached over Tailscale with its
defaults, every client may reach nginx with one address, the network's
gateway `172.31.250.1`, and the limit then acts per account. This is accepted
([decision #152](shared/docs/decisions/README.md#register)); whoever runs the
machine can restore real addresses on their own. There are two causes:

| Cause | Why |
|---|---|
| Tailscale's source NAT | `tailscaled` masquerades traffic it forwards from the tailnet, Docker's published ports included. It applies when Tailscale set its firewall rules after Docker, so it can change with every restart of either. |
| IPv6 | The stack's network is IPv4 only, so Docker passes IPv6 connections through `docker-proxy`, which connects to nginx from the gateway. |

To give the api each device's real tailnet address:

1. Turn off Tailscale's source NAT on the host:
   `tailscale set --snat-subnet-routes=false`. It is kept across restarts. If
   the machine is also a subnet router or an exit node, devices on its local
   network then need a route back to the tailnet.
2. Publish 443 on IPv4 only: in the node folder's `compose.yaml`, change the
   nginx port to `"0.0.0.0:443:8443"`. Clients fall back to IPv4. An update
   that replaces the compose file undoes this change, so check it after one.

The other way, `tailscale serve --tcp 443 --proxy-protocol 2` in front of
nginx, needs a PROXY listener in nginx that the node does not have yet.

In the tailnet's access policy, a grant that lets only your own devices and
the people you share the node with reach `tcp:443` on it keeps everyone else
from trying a password at all.

To check, as root on the node, while another tailnet device opens
`https://<node's tailnet name>/api/v1/me` with `curl -4` and then `curl -6`:

1. `iptables -t nat -S ts-postrouting`: with the source NAT off it shows no
   `MASQUERADE`.
2. `tcpdump -nni br-<the titan network's id> 'tcp dst port 8443 and tcp[tcpflags] & tcp-syn != 0'`:
   the source is the device's `100.x` address, not `172.31.250.1`; `curl -6`
   is refused.
3. Repeat after `systemctl restart tailscaled` and again after
   `systemctl restart docker`: the order of restarts must not change the
   source.

## Node test

```sh
scripts/node-test.sh <empty node folder> <titan-backend checkout>
```

It builds the backend's images and the controller's, installs the node with
a self-signed certificate for `localhost`, starts the stack, creates the
owner, and checks signing in, the password-guessing limit by the client's
address (believed only from nginx), nginx's sign-in rate limit, the security
headers, TLS 1.2 and 1.3 (1.1 refused), HTTP/2 and that only 443 is
published, that nginx logs JSON lines without query strings, and that nginx
reaches a recreated api. For the controller it checks that it has no
network, its socket's owner and mode, that only the api mounts the socket
and other users there are refused, `docker compose ps` from inside it, and
`GET /health` over the socket from the api, with nginx running, then
stopped, then removed. Its requests come over loopback,
so it proves the mechanism of the client address; which address a real
client has on a node reached over Tailscale is checked by hand before a
release ([decision #82](shared/docs/decisions/README.md#register)). It needs
an empty node folder and refuses any other, and makes it readable by
everyone (`0755`) for the controller. Whatever happens, it then removes the
stack, its volumes and the images it built; the files it wrote into the node
folder stay. Nothing is pushed or published.

## Getting the code

```sh
git clone --recurse-submodules https://github.com/titan-home/titan-node.git
# after a pull:
git submodule update --init
```

## Licence

Released into the public domain under [the Unlicense](LICENSE).
