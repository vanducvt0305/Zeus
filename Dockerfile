# Build the MCP discovery server into a tiny static image.
#   docker build -t ghcr.io/vanducvt0305/zeus .
#
# The server talks MCP over stdio and needs a reachable Qdrant (see
# docker-compose.yml). Pass configuration via -e, e.g.:
#   docker run -i --rm -e QDRANT_HOST=host.docker.internal ghcr.io/vanducvt0305/zeus
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
# stdio transport: keep stdin/stdout clean for the MCP client.
ENTRYPOINT ["/server"]
