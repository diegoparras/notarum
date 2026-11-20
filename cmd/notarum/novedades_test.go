package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/diegoparras/notarum"
	"github.com/diegoparras/notarum/internal/novedades"
)

// TestChangelogTieneLaVersionActual: cada deploy dice qué cambia. La versión
// del código tiene que tener su sección en el CHANGELOG.md, con al menos un
// ítem, y ser la primera (la más nueva). El build de la imagen corre los
// tests, así que sin esto la imagen no se construye.
func TestChangelogTieneLaVersionActual(t *testing.T) {
	vs := novedades.Parsear(notarum.Changelog)
	actual, ok := novedades.Buscar(vs, version)
	if !ok {
		t.Fatalf("falta \"## [%s]\" en CHANGELOG.md", version)
	}
	if len(actual.Secciones) == 0 {
		t.Fatalf("la sección %s del CHANGELOG está vacía", version)
	}
	if vs[0].Version != version {
		t.Errorf("la primera versión del CHANGELOG es %s y el código dice %s", vs[0].Version, version)
	}
}

// TestCartelDeArranque: lo primero que escribe `notarum servir` es la versión
// que corre y qué cambia, sin renglones de más de 80 columnas.
func TestCartelDeArranque(t *testing.T) {
	var b bytes.Buffer
	imprimirCartel(&b)
	lineas := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lineas) < 4 {
		t.Fatalf("cartel demasiado corto: %q", lineas)
	}
	if !strings.HasPrefix(lineas[1], "NOTARUM v"+version) {
		t.Errorf("el cartel no arranca con la versión: %q", lineas[1])
	}
	if lineas[2] != "Que cambia en esta version:" {
		t.Errorf("el cartel no dice qué cambia: %q", lineas[2])
	}
	for _, l := range lineas {
		if n := len([]rune(l)); n > 80 {
			t.Errorf("renglón de %d columnas: %q", n, l)
		}
	}
}

// TestNotas: `notarum notas` imprime en markdown las notas de la versión que
// corre (las usa el CI para el Release) y falla si el CHANGELOG no las tiene.
func TestNotas(t *testing.T) {
	var b bytes.Buffer
	if err := imprimirNotas(&b, version); err != nil {
		t.Fatalf("imprimirNotas(%s): %v", version, err)
	}
	if !strings.HasPrefix(b.String(), "### ") {
		t.Errorf("las notas no arrancan con una sección: %q", b.String())
	}
	if err := imprimirNotas(&bytes.Buffer{}, "99.0.0"); err == nil {
		t.Error("una versión sin sección en el CHANGELOG no tiene que salir bien")
	}
}
