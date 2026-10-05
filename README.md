# titan-node

The TITAN node: the controller written in Go, the docker compose file, the nginx configuration, and everything needed to install and update a node on a machine.

Part of TITAN, a self-hosted personal AI assistant, planner and tracker. The
product, architecture and rules shared by every TITAN repository are in the
`shared/` submodule ([titan-shared](shared/README.md)).

## Contents

- **Controller** (Go): service health, image updates by digest and rollback, scheduled database backups, node secrets, certificates, running plugins; later the cluster.
- **compose file**: nginx, web-ui, api, worker, controller, PostgreSQL, embeddings.
- **nginx configuration**: the single entry, TLS, `/` → UI files, `/api` → API, security headers, SSE without buffering.
- **Install and update scripts** for a clean machine.

## Status

Not started. Build-plan stage 4. See the [build plan](shared/docs/roadmap/plan.md).

## Getting the code

```sh
git clone --recurse-submodules https://github.com/titan-home/titan-node.git
# after a pull:
git submodule update --init
```

## Licence

Released into the public domain under [the Unlicense](LICENSE).
