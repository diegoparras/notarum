# --- compilación -------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Las dependencias primero: si no cambian, esta capa se reusa.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=1.0.0
# CGO apagado: el binario queda estático y corre en una imagen mínima.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/notarum ./cmd/notarum

# La suite corre en el build: una imagen no se publica con tests rojos.
RUN go test ./...

# --- imagen final ------------------------------------------------------------
FROM alpine:3.20

# Certificados para hablar HTTPS con boletinoficial.gob.ar; wget para el healthcheck.
RUN apk add --no-cache ca-certificates wget && \
    adduser -D -u 10001 notarum && \
    mkdir -p /datos/cache && \
    chown -R notarum:notarum /datos

COPY --from=build /out/notarum /usr/local/bin/notarum

USER notarum
WORKDIR /datos

ENV NOTARUM_PUERTO=8080 \
    NOTARUM_CACHE=/datos/cache \
    NOTARUM_POR_MINUTO=60 \
    NOTARUM_INTERVALO=500ms \
    NOTARUM_LOG=json

EXPOSE 8080

# La caché vive acá: montá un volumen para no volver a bajar lo ya bajado.
VOLUME ["/datos"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/v1/salud >/dev/null || exit 1

ENTRYPOINT ["notarum"]
CMD ["servir"]
