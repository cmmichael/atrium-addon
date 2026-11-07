# Built in GitHub Actions for amd64 and aarch64 (tools/addon-build.yaml) and pulled by the Supervisor, so an
# install takes seconds. The Supervisor can also build it on the machine itself, when config.yaml has no image.
# The Go build runs on the build machine's own architecture and cross-compiles, so the arm64 image needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETARCH
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /atrium .

FROM alpine:3.22
ARG BUILD_VERSION
ARG BUILD_ARCH
LABEL io.hass.name="Atrium" io.hass.type="addon" io.hass.version="$BUILD_VERSION" io.hass.arch="$BUILD_ARCH" \
      org.opencontainers.image.source="https://github.com/cmmichael/atrium-addon" \
      org.opencontainers.image.description="Atrium for Home Assistant: your house in the Atrium app and on the web, over your tailnet"
RUN apk add --no-cache ca-certificates python3
COPY --from=build /atrium /usr/local/bin/atrium
# The built web app and the atrium-mapping skill (web/ and skills/ in the repository; tools/build-web.sh copies
# them into this folder). Python runs the skill's draft script for the MCP server's atrium_draft tool.
COPY web/ /app/web/
COPY skill/ /app/skill/
CMD ["/usr/local/bin/atrium"]
