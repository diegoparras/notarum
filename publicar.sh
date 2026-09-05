#!/usr/bin/env bash
# Publica la imagen de notarum en ghcr.io.
#
#   bash publicar.sh
#
# Pide el token una sola vez, en la pantalla de docker login. No lo guarda en
# ningún archivo ni lo imprime: queda en el credential store de Docker, y los
# push siguientes ya no lo piden.
#
# El token se crea en:
#   https://github.com/settings/tokens/new?scopes=write:packages,read:packages
set -euo pipefail

USUARIO=diegoparras
IMAGEN=ghcr.io/$USUARIO/notarum
VERSION=1.7.2

echo "==> Sesión en ghcr.io"
if docker system info 2>/dev/null | grep -q "ghcr.io"; then
  echo "    ya había sesión abierta"
else
  echo "    pegá el token cuando pida la contraseña (no se ve al escribir)"
  docker login ghcr.io -u "$USUARIO"
fi

echo "==> Construyendo $IMAGEN:$VERSION"
docker build -t "$IMAGEN:$VERSION" -t "$IMAGEN:latest" --build-arg VERSION="$VERSION" .

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
