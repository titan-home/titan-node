# The controller's image (decisions #4, #142): the controller, built from
# source into a static binary, on the official Docker CLI image, which holds
# `docker` and the compose plugin. Base images are pinned by digest
# (development rules, 11). Build it with `docker build -t titan-controller .`.

FROM golang:1.27.2-alpine3.24@sha256:85dc1069ac644ea3c527b177303a406eb3358192816cd7f9e5848eb658851673 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd cmd
COPY internal internal
RUN CGO_ENABLED=0 go build -trimpath -o /titan-controller ./cmd/titan-controller

# Docker CLI 29.8.2, the same version as the reference node's Docker Engine,
# with Docker Compose 5.5.1: the build of 2026-09-30, since the later builds
# carry Compose 5.6.0, younger than the 14 days every new version waits
# (development rules, 11).
FROM docker:29.8.2-cli@sha256:b1805116a6a86cc591b5d5f60a910a0715cdcc9d18d866ad68b1457ead25c35c
COPY --from=build /titan-controller /usr/local/bin/titan-controller
# The folder for the controller's socket, owned by its user and the api's
# group. An empty named volume mounted here takes this owner and mode.
RUN install -d -o 10002 -g 10001 -m 0750 /run/titan-controller
USER 10002:10001
ENTRYPOINT ["/usr/local/bin/titan-controller"]
CMD []
