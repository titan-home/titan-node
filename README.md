# titan-node

The TITAN node: the controller written in Go, the docker compose file, the nginx configuration, and everything needed to install and update a node on a machine.

Part of TITAN, a self-hosted personal AI assistant, planner and tracker. The
product, architecture and rules shared by every TITAN repository are in the
`shared/` submodule ([titan-shared](shared/README.md)).

## Contents

- **Controller** (Go): service health, image updates by digest and rollback, scheduled database backups, node secrets, certificates, running plugins; later the cluster.
- **compose file**: nginx, api, migrate and PostgreSQL now; web-ui, worker, controller and embeddings later.
- **nginx configuration**: the single entry, TLS, `/` → UI files, `/api` → API, security headers, SSE without buffering.
- **Install and update scripts** for a clean machine.

## Status

Build-plan stage 4, in progress: the node's compose file, nginx with TLS and
the node test exist; the controller does not yet. See the
[build plan](shared/docs/roadmap/plan.md).

## Layout

`node/` holds exactly what is installed into the node folder, `/opt/titan`
([decision #148](shared/docs/decisions/README.md#register)).

| Path | What it is |
|---|---|
| `node/compose.yaml` | The stack: `nginx`, `api`, `migrate` (applies migrations, then exits) and `db` |
| `node/nginx/titan.conf` | nginx: TLS on 443, `/api/` to the API, `/` answers 404 until the web UI arrives, security headers |
| `scripts/node-test.sh` | The node test: installs `node/` into a node folder, starts the stack and checks it |
| `.github/workflows/node.yml` | Runs the node test on every pull request |

Beside `compose.yaml` the node folder holds what is not in git:

| Path | What it is |
|---|---|
| `.env` | `TITAN_API_IMAGE` and `TITAN_ADMIN_IMAGE`, the backend's images |
| `secrets/db_password`, `secrets/claude_token` | The secrets, read through `*_FILE` variables |
| `tls/cert.pem`, `tls/key.pem` | The certificate and its key |

## Run the stack by hand

Until the controller installs a node, the steps are the node test's. It needs
Docker, port 443 free, and a `titan-backend` checkout to build the images
from, since none are published yet
([decision #149](shared/docs/decisions/README.md#register)).

1. Build the images:
   `docker build --target api --tag titan-api:local ../titan-backend` and the
   same with `--target admin --tag titan-admin:local`.
2. Copy `node/` into the node folder: `cp -R node/. /opt/titan/`.
3. In the node folder, write `.env`:
   `TITAN_API_IMAGE=titan-api:local` and `TITAN_ADMIN_IMAGE=titan-admin:local`,
   one per line.
4. Create `secrets/` and `tls/` with mode `0700`; put a random password in
   `secrets/db_password`, the Claude token (or nothing) in
   `secrets/claude_token`, and the certificate and key in `tls/`. Make the
   four files readable (`0644`): the containers read them as other users.
5. Start the stack: `docker compose up --wait`.
6. Create the owner: `docker compose run --rm migrate titan-admin create-owner`.

The API answers on `https://<host name>/api/`. Stop the stack with
`docker compose down`; `--volumes` also deletes the database.

## Node test

```sh
scripts/node-test.sh <empty node folder> <titan-backend checkout>
```

It builds the backend's images, installs the node with a self-signed
certificate for `localhost`, starts the stack, creates the owner, and checks
signing in, the security headers, TLS 1.2 and 1.3 (1.1 refused), HTTP/2 and
that only 443 is published, that nginx logs JSON lines without query
strings, and that nginx reaches a recreated api. It needs an empty node folder
and refuses any other. Whatever happens, it then removes the stack, its volume
and the images it built; the files it wrote into the node folder stay. Nothing
is pushed or published.

## Getting the code

```sh
git clone --recurse-submodules https://github.com/titan-home/titan-node.git
# after a pull:
git submodule update --init
```

## Licence

Released into the public domain under [the Unlicense](LICENSE).
