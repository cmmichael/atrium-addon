# Built by the Supervisor on the Home Assistant machine itself (a few minutes the first time).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /atrium .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /atrium /usr/local/bin/atrium
# The built web app (web/ in the repository, built by tools/build-web.sh into this folder).
COPY web/ /app/web/
CMD ["/usr/local/bin/atrium"]
