# The whole product as one image: the core and the web apps it serves (the
# studio, the candidate runner under /take/, the credential check under
# /verify/). Build from the repository root:
#
#   docker build -f deploy/Dockerfile -t assessmatrix:dev .
#
# Same image for cloud and self-host; `migrate` and `serve` are its commands.

FROM oven/bun:1.4.2 AS web
WORKDIR /src
COPY package.json bun.lock ./
COPY apps/studio/package.json apps/studio/
COPY apps/verify/package.json apps/verify/
COPY packages/proto/package.json packages/proto/
RUN bun install --frozen-lockfile --ignore-scripts
COPY packages/proto packages/proto
COPY apps apps
WORKDIR /src/apps/studio
ENV VITE_BACKEND=core
RUN bunx vite build --outDir /web --emptyOutDir \
 && bunx vite build -c vite.runner.config.ts --outDir /web \
 && bunx vite build -c ../verify/vite.config.ts --outDir /web/verify

FROM golang:1.27.3 AS core
WORKDIR /src
COPY services/core/go.mod services/core/go.sum ./
RUN go mod download
COPY services/core .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /core ./cmd/core
RUN mkdir -p /data/media

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=core /core /core
COPY --from=web /web /web
COPY --from=core --chown=65532:65532 /data /data
ENV AM_STATIC_DIR=/web
USER nonroot:nonroot
# 8080: the app. 9090: Prometheus metrics, for the cluster only.
EXPOSE 8080 9090
ENTRYPOINT ["/core"]
CMD ["serve"]
