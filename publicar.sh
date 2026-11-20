#!/usr/bin/env bash
# Publica la imagen de notarum en ghcr.io, a mano.
#
#   bash publicar.sh
#
# No hace falta para el camino de siempre: la imagen se publica sola cada vez
# que algo entra en main y pasa las pruebas. Esto queda para cuando haya que
# publicar sin pasar por ahí. Publica lo que haya en esta carpeta, esté o no
# confirmado.
#
# Pide el token una sola vez, en la pantalla de docker login. No lo guarda en
# ningún archivo ni lo imprime: queda en el credential store de Docker, y los
# push siguientes ya no lo piden.
#
# El token se crea en:
#   https://github.com/settings/tokens/new?scopes=write:packages,read:packages
set -euo pipefail

cd "$(dirname "$0")"

USUARIO=diegoparras
IMAGEN=ghcr.io/$USUARIO/notarum

# La versión sale del código, del mismo lugar que la lee la publicación
# automática. Escrita a mano acá se quedó dos versiones atrás sin que nadie lo
# notara: habría publicado el código de hoy con el número de antes.
VERSION=$(sed -n 's/^var version = "\([^"]*\)".*/\1/p' cmd/notarum/main.go)
if [ -z "$VERSION" ]; then
  echo "No se pudo leer la versión de cmd/notarum/main.go" >&2
  exit 1
fi

# Ninguna versión sale sin decir qué cambia: la sección "## [X.Y.Z]" del
# CHANGELOG.md, con al menos un ítem. Es la misma regla que el CI (y que el
# test que corre adentro del build), revisada acá antes de pedir el token.
if ! awk -v v="$VERSION" '
  index($0, "## [" v "]") == 1 { adentro = 1; next }
  adentro && /^## / { exit }
  adentro && /^- / { hay = 1 }
  END { exit hay ? 0 : 1 }
' CHANGELOG.md; then
  echo "CHANGELOG.md no tiene la sección \"## [$VERSION]\" (o está vacía): agregala antes de publicar." >&2
  exit 1
fi

echo "==> Sesión en ghcr.io"
if docker system info 2>/dev/null | grep -q "ghcr.io"; then
  echo "    ya había sesión abierta"
else
  echo "    pegá el token cuando pida la contraseña (no se ve al escribir)"
  docker login ghcr.io -u "$USUARIO"
fi

echo "==> Construyendo $IMAGEN:$VERSION"
docker build -t "$IMAGEN:$VERSION" -t "$IMAGEN:latest" --build-arg VERSION="$VERSION"   --label org.opencontainers.image.version="$VERSION" .

echo "==> Publicando"
docker push "$IMAGEN:$VERSION"
docker push "$IMAGEN:latest"

echo
echo "Listo. La imagen quedó en $IMAGEN:$VERSION"
echo
echo "Falta un paso a mano: un paquete recién publicado en ghcr nace privado."
echo "Para que EasyPanel pueda bajarlo sin credenciales, hacelo público en:"
echo "  https://github.com/users/$USUARIO/packages/container/notarum/settings"
echo "  → Change visibility → Public"
echo
echo "Después, en EasyPanel: + Service → App, Type: Image,"
echo "  Image: $IMAGEN:$VERSION"
