package main

import (
	"fmt"
	"io"

	"github.com/diegoparras/notarum"
	"github.com/diegoparras/notarum/internal/novedades"
)

// anchoCartel es el ancho máximo de cada renglón del cartel de arranque: lo
// que entra sin scroll lateral en los registros de EasyPanel.
const anchoCartel = 80

// imprimirCartel escribe la versión que corre y qué cambia (del CHANGELOG
// embebido). Es lo PRIMERO que sale al levantar `notarum servir`, en texto
// plano aunque el log sea JSON: es para leerlo de un vistazo en los registros
// del deploy. Nunca frena el arranque: sin notas, lo dice y sigue.
func imprimirCartel(w io.Writer) {
	for _, l := range novedades.Cartel("NOTARUM", version, novedades.Parsear(notarum.Changelog), anchoCartel) {
		fmt.Fprintln(w, l)
	}
}

// imprimirNotas escribe en markdown las notas de `v`, o devuelve error si el
// CHANGELOG no tiene su sección. Es `notarum notas`: el CI lo usa para el
// Release y se niega a publicar sin notas.
func imprimirNotas(w io.Writer, v string) error {
	nota, ok := novedades.Buscar(novedades.Parsear(notarum.Changelog), v)
	if !ok || len(nota.Secciones) == 0 {
		return fmt.Errorf("CHANGELOG.md no tiene la sección \"## [%s]\" (o está vacía): agregala antes de publicar", v)
	}
	_, err := fmt.Fprint(w, novedades.NotasMarkdown(nota))
	return err
}
