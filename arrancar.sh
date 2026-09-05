#!/bin/sh
# Arranque del contenedor: deja el directorio de datos usable y baja de root.
#
# El volumen que monta un panel de despliegue viene siendo de root, y notarum
# corre como un usuario sin privilegios. Sin esto, montar el volumen —que es
# justo lo que hay que hacer para no perder los datos en cada despliegue— haría
# que notarum no pudiera escribir y no llegara a arrancar, con un error de
# permisos que no dice qué hacer.
#
# Se corre como root sólo el tiempo de acomodar los permisos, y se baja a
# notarum antes de ejecutar nada del servicio.
set -e

datos="${NOTARUM_DATOS:-/datos}"

if [ "$(id -u)" = "0" ]; then
	mkdir -p "$datos/cache"
	# Sólo si hace falta: con el motor de disco acá adentro puede haber
	# cientos de miles de archivos, y recorrerlos en cada arranque sería
	# pagar todos los días por algo que se hace una vez.
	if [ "$(stat -c %u "$datos")" != "10001" ]; then
		echo "acomodando los permisos de $datos" >&2
		chown -R notarum:notarum "$datos"
	fi
	exec su-exec notarum "$@"
fi

# Ya se está corriendo sin privilegios: no hay nada que acomodar.
exec "$@"
