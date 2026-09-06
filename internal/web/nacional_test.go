package web

import (
	"net/http"
	"strings"
	"testing"
)

// La normativa nacional tiene que poder buscarse desde la interfaz, igual que
// la provincial: tener la ruta en la API y no la página es dejar la mitad
// afuera, porque quien mira el sitio no va a armar un curl para ver una ley.
func TestElBuscadorNacionalEstaEnLaInterfaz(t *testing.T) {
	srv := sitioDePrueba(t)
	res, html := pedir(t, srv, "/nacional")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("contestó %d", res.StatusCode)
	}
	if !strings.Contains(html, "Normativa nacional") {
		t.Error("no es la página de normativa nacional")
	}
}

// Y cuando el buscador está apagado —que es como viene, porque el índice
// ocupa— la página lo dice y dice cómo encenderlo, en vez de mostrar un
// formulario que no va a encontrar nada.
func TestLaPaginaNacionalDiceComoEncenderElBuscador(t *testing.T) {
	srv := sitioDePrueba(t)
	_, html := pedir(t, srv, "/nacional")

	if !strings.Contains(html, "NOTARUM_BUSCADOR_INFOLEG") {
		t.Error("no dice cómo encender el buscador")
	}
	if strings.Contains(html, `name="texto"`) {
		t.Error("muestra un formulario que no puede buscar nada")
	}
}

// Y se llega desde el menú, que es de donde se llega a todo lo demás.
func TestSeLlegaALaNacionalDesdeElMenu(t *testing.T) {
	srv := sitioDePrueba(t)
	// Cualquier página sirve: el menú está en la plantilla de base.
	_, html := pedir(t, srv, "/docs")

	if !strings.Contains(html, `href="/nacional"`) {
		t.Error("el menú no lleva a la normativa nacional")
	}
}
