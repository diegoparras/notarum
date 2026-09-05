package asistente

import (
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/contrato"
	"github.com/diegoparras/notarum/internal/mcp"
)

// El contexto sale del contrato y de las herramientas, así que no puede
// quedar desactualizado: si mañana se agrega una ruta, tiene que aparecer sin
// que nadie escriba nada acá.
func TestElContextoTraeTodaLaAPI(t *testing.T) {
	ins, err := Instrucciones("https://notarum.ejemplo.ar")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := contrato.Leer()
	if err != nil {
		t.Fatal(err)
	}
	var rutas int
	for _, g := range doc.Grupos {
		for _, r := range g.Rutas {
			rutas++
			if !strings.Contains(ins, r.Camino) {
				t.Errorf("falta la ruta %s en el contexto", r.Camino)
			}
		}
	}
	if rutas == 0 {
		t.Fatal("el contrato no tiene rutas")
	}
	t.Logf("rutas en el contexto: %d | tamaño: %d caracteres", rutas, len(ins))
}

func TestElContextoTraeTodasLasHerramientas(t *testing.T) {
	ins, err := Instrucciones("https://notarum.ejemplo.ar")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range mcp.Herramientas() {
		if !strings.Contains(ins, h.Nombre) {
			t.Errorf("falta la herramienta %s", h.Nombre)
		}
	}
}

// Los parámetros también: sin ellos el modelo inventa nombres.
func TestElContextoTraeLosParametros(t *testing.T) {
	ins, err := Instrucciones("https://x")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"seccion", "fecha", "texto", "provincia", "vigentes", "rubro"} {
		if !strings.Contains(ins, p) {
			t.Errorf("falta el parámetro %q", p)
		}
	}
}

// La dirección de la instancia entra, para que los ejemplos sean copiables.
func TestElContextoUsaLaDireccionDeLaInstancia(t *testing.T) {
	ins, err := Instrucciones("https://notarum.ejemplo.ar")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ins, "https://notarum.ejemplo.ar") {
		t.Error("no dice cuál es la dirección de esta instancia")
	}
	if !strings.Contains(ins, "/cuenta") {
		t.Error("no dice dónde se crean los tokens")
	}
}

// Y las reglas de forma: sin ellas el modelo contesta con tres párrafos de
// introducción antes del código.
func TestElContextoPideSoloElCodigo(t *testing.T) {
	ins, _ := Instrucciones("https://x")
	for _, regla := range []string{"nada más", "No inventes", "AAAA-MM-DD", "curl", "n8n"} {
		if !strings.Contains(ins, regla) {
			t.Errorf("falta la regla sobre %q", regla)
		}
	}
}

// No puede crecer sin control: es lo que se paga en cada consulta.
func TestElContextoEsRazonable(t *testing.T) {
	ins, _ := Instrucciones("https://x")
	// Unos 4 caracteres por token: 40 mil caracteres son ~10 mil tokens, que
	// ya es caro para una tarea que es traducir un pedido.
	if len(ins) > 40000 {
		t.Errorf("el contexto son %d caracteres: cada consulta lo paga entero", len(ins))
	}
	if len(ins) < 2000 {
		t.Errorf("el contexto son sólo %d caracteres: parece que falta algo", len(ins))
	}
}

func TestUnaLinea(t *testing.T) {
	if got := unaLinea("una\n  cosa\tcon   espacios\n"); got != "una cosa con espacios" {
		t.Errorf("= %q", got)
	}
}
