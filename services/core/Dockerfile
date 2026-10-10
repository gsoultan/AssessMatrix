# The core as one static binary on a distroless base: no shell, no package
# manager, non-root. The same image serves cloud and self-host.
FROM golang:1.27.1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /core ./cmd/core

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /core /core
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/core"]
CMD ["serve"]
