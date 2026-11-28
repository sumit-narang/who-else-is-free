FROM golang:1.25 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

# Cover images are not committed (public repo); fetch the ones listed in
# covers_catalog.json from Drive. Kept in its own layer that depends only on
# the catalog and the sync tool, so the download re-runs only when the
# catalog changes — code-only deploys reuse the cached layer.
COPY covers_catalog.json ./
COPY cmd/covers-sync ./cmd/covers-sync
RUN go run ./cmd/covers-sync -fetch

# IP-to-city database for /api/location/approximate (DB-IP City Lite, about
# 130 MB, not committed). GEOIP_EDITION only busts this layer's cache: the
# deploy workflow passes the current month so a new edition is picked up once
# a month, and other deploys reuse the cached layer.
ARG GEOIP_EDITION=
COPY cmd/geoip-sync ./cmd/geoip-sync
RUN echo "geoip edition: ${GEOIP_EDITION:-latest}" && go run ./cmd/geoip-sync

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server .

FROM gcr.io/distroless/base-debian12

WORKDIR /app
COPY --from=builder /app/server /app/server
COPY --from=builder /app/assets /app/assets

EXPOSE 8080

CMD ["/app/server"]
