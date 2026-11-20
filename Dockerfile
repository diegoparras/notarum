# --- compilación -------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Las dependencias primero: si no cambian, esta capa se reusa.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=1.8.0
# CGO apagado: el binario queda estático y corre en una imagen mínima.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/notarum ./cmd/notarum

# La suite corre en el build: una imagen no se publica con tests rojos.
RUN go test ./...

# --- imagen final ------------------------------------------------------------
FROM alpine:3.20

# Certificados para HTTPS; wget para el healthcheck; su-exec para bajar de root
# una vez acomodados los permisos del volumen.
RUN apk add --no-cache ca-certificates wget su-exec && \
    adduser -D -u 10001 notarum && \
    mkdir -p /datos/cache && \
    chown -R notarum:notarum /datos

# El origen, para que el registro sepa de qué repositorio salió esta imagen:
# es lo que la deja vinculada al repo y con su procedencia a la vista.
LABEL org.opencontainers.image.source="https://github.com/diegoparras/notarum" \
      org.opencontainers.image.description="API abierta del Boletin Oficial de la Republica Argentina" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/notarum /usr/local/bin/notarum
COPY arrancar.sh /usr/local/bin/arrancar
RUN chmod +x /usr/local/bin/arrancar

# Sin USER: el arranque necesita ser root para acomodar los permisos del
# volumen montado, y baja a notarum antes de ejecutar el servicio.
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

ENTRYPOINT ["/usr/local/bin/arrancar", "notarum"]
CMD ["servir"]
