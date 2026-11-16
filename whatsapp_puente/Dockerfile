# syntax=docker/dockerfile:1
# Compila el puente (Go + whatsmeow + SQLite) y lo deja en una imagen pequeña.

FROM golang:1.26-alpine AS build
RUN apk add --no-cache gcc musl-dev
ENV CGO_ENABLED=1 GOTOOLCHAIN=local
WORKDIR /src
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
RUN go build -trimpath -ldflags="-s -w" -o /out/puente .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/puente /usr/bin/puente

ARG BUILD_VERSION
LABEL \
    io.hass.name="WhatsApp puente" \
    io.hass.description="Guarda el texto de tus conversaciones de WhatsApp y programa envíos desde Home Assistant" \
    io.hass.type="addon" \
    io.hass.version="${BUILD_VERSION}" \
    org.opencontainers.image.title="WhatsApp puente" \
    org.opencontainers.image.source="https://github.com/MrGuas/ha-apps" \
    org.opencontainers.image.licenses="MIT"

CMD ["/usr/bin/puente"]
