ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:latest
LABEL maintainer="The Prometheus Authors <prometheus-developers@googlegroups.com>"
# MCP registry ownership label. Must stay in sync with `name` in server.json.
LABEL io.modelcontextprotocol.server.name="io.github.prometheus/prometheus-mcp"

ARG ARCH="amd64"
ARG OS="linux"
COPY .build/${OS}-${ARCH}/prometheus-mcp /bin/prometheus-mcp

USER nobody
EXPOSE     8080
ENTRYPOINT [ "/bin/prometheus-mcp" ]
