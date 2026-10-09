# CLAUDE.md: titan-node

The TITAN node: the controller written in Go, the docker compose file, the nginx configuration, and everything needed to install and update a node on a machine.

Read and follow, in this order:

1. [Rules for AI agents](shared/docs/development/ai-agents.md) — what you may
   and may not do. They override your defaults.
2. [Development rules](shared/docs/development/rules.md) — how we work, for
   every repository.
3. [Documentation index](shared/docs/README.md) — product, architecture,
   decisions, build plan.

`shared/` is the `titan-shared` submodule. Never edit files under it from this
repository; change `titan-shared` through its own pull request.

## Stack and commands

- Go, version pinned in `go.mod`; `gofmt`, `go vet`, `golangci-lint`, `go test`.
- Talks to Docker through its socket: the most sensitive privilege on the node; keep that code small, reviewed and tested.

| What | Command |
|---|---|
| The node test: build titan-backend's images, install `node/` into an empty node folder, start the stack and check it, then remove the stack, its volume and the built images; the files it wrote into the folder stay (needs Docker, port 443 and the subnet `172.31.250.0/24` free) | `scripts/node-test.sh <empty node folder> <titan-backend checkout>` |

The node test runs in CI on every pull request
(`.github/workflows/node.yml`, node folder `/opt/titan`). Elsewhere, give it
an empty temporary folder on a machine with Docker; it refuses a folder that
is not empty.

Other commands are added here as the code arrives.

## Rules specific to this repository

- The controller is the owner's first Go project: prepare scaffolding, failing tests and explanations; the owner writes the logic (see the AI rules).
- Every change to the compose file or nginx config is checked by a node test that starts the stack.
- Never weaken the nginx security headers, TLS settings or sign-in rate
  limit, and never let the api believe `X-Forwarded-For` from anything but
  nginx's fixed address, without a decision in the register.

## Before committing

Run this checklist before every commit
([development rules, "Before committing"](shared/docs/development/rules.md#before-committing)).
The commands are settled as the code arrives.

1. `gofmt -l .` prints nothing; `go vet ./...` and `golangci-lint run` pass.
2. `go test` passes for every package the commit touches.
3. If anything under `node/` or `scripts/node-test.sh` changed: the node test
   passes: `scripts/node-test.sh <empty node folder> <titan-backend checkout>`.
4. If the Docker access code changed: its tests pass and the change is called
   out in the commit message.
5. If Markdown or the `shared/` pointer changed:
   `python3 shared/scripts/check_links.py .` prints nothing.
