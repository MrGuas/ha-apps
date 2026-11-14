# Compila el puente (Go + whatsmeow + SQLite) y lo deja en una imagen pequeña
FROM golang:1-alpine AS build
RUN apk add --no-cache gcc musl-dev git
ENV GOTOOLCHAIN=auto CGO_ENABLED=1
WORKDIR /src
COPY app/ ./
RUN go mod tidy && go build -trimpath -ldflags="-s -w" -o /out/puente .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/puente /usr/bin/puente
CMD ["/usr/bin/puente"]
