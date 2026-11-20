# Changelog

Formato basado en [Keep a Changelog](https://keepachangelog.com/es/1.0.0/).
Versionado semántico. Cada versión que se publica tiene su sección acá: es lo
que muestran el arranque del contenedor, la página de novedades del lector,
el Release de GitHub y `notarum notas`.

## [1.8.2] - 2026-11-20

### Nuevo
- **Cada deploy dice qué versión es y qué cambia**: lo primero que escribe el
  contenedor en los registros de EasyPanel es un cartel con la versión, la
  fecha y sus novedades, cortado al ancho para leerlo sin scroll lateral.
- **Novedades en el lector**: el menú tiene **novedades**, el pie enlaza
  **qué cambió** al lado de la versión, y quien vuelve después de una
  actualización ve una sola vez un aviso con lo que cambió desde la última
  versión que vio en ese navegador.
- **Cada versión publicada tiene su Release** en GitHub (`vX.Y.Z`) con estas
  notas, y la imagen lleva la versión en la etiqueta OCI. `notarum notas`
  imprime en markdown las notas de la versión que corre.

### Cambiado
- La imagen no se publica si este CHANGELOG no tiene la sección de su versión,
  ni desde el CI ni con `publicar.sh`.
- CI: acciones de GitHub actualizadas a sus versiones que corren sobre
  Node 24 (checkout v7, setup-go v7, login v4, setup-buildx v4,
  build-push v7).
