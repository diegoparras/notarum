package vencimientos

import (
	"strings"
	"unicode/utf8"
)

// DeLatin1 convierte un cuerpo ISO-8859-1, que es como viene la página, a
// UTF-8. Leerlo como UTF-8 no falla: deja "Cr\xe9dito" adentro del texto, y así
// entra a la base. En ISO-8859-1 cada byte es el punto de código del mismo
// número, así que la conversión es rune(b) y no hace falta ninguna dependencia.
//
// El encabezado de la página ya trae, escritos por ARCA, los bytes del
// carácter de reemplazo; se convierten tal cual y el parseo no depende de ese
// texto.
func DeLatin1(b []byte) string {
	if esASCII(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) * 2)
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

func esASCII(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
