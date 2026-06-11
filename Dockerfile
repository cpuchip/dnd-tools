# dnd-tools service image: the JSON API + MCP-over-HTTP at /mcp, state in
# a SQLite file on the /data volume. Built straight from the public repo
# (compose can use this repo URL as the build context).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dnd-mcp ./cmd/dnd-mcp

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/dnd-mcp /usr/local/bin/dnd-mcp
ENV DND_DB=/data/dnd.db \
    DND_HTTP=:8089
VOLUME /data
EXPOSE 8089
# DND_API_KEY comes from the deployment environment.
ENTRYPOINT ["/usr/local/bin/dnd-mcp", "-serve"]
