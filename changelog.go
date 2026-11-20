// Package notarum es la raíz del módulo: lo único que vive acá es el
// CHANGELOG.md embebido, para que el binario lleve adentro lo que tiene que
// decir al levantar (internal/novedades) sin depender de que el archivo esté
// en la imagen. La versión sigue en cmd/notarum/main.go (`var version`), que
// es de donde la leen el CI y publicar.sh.
package notarum

import _ "embed"

// Changelog es el CHANGELOG.md del repositorio, embebido al compilar.
//
//go:embed CHANGELOG.md
var Changelog string
