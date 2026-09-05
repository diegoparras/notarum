package asistente

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const catalogoDePrueba = `{"data":[
  {"id":"anthropic/claude-3.5-haiku","name":"Anthropic: Claude 3.5 Haiku","context_length":200000,
   "architecture":{"output_modalities":["text"]},
   "pricing":{"prompt":"0.0000008","completion":"0.000004"}},
  {"id":"z-ai/glm-4.5-air:free","name":"Z.ai: GLM 4.5 Air (gratis)","context_length":131072,
   "architecture":{"output_modalities":["text"]},
   "pricing":{"prompt":"0","completion":"0"}},
  {"id":"un/dibujante","name":"Un dibujante","context_length":4096,
   "architecture":{"output_modalities":["image"]},
   "pricing":{"prompt":"0.001","completion":"0.001"}},
  {"id":"openrouter/auto","name":"Auto Router","context_length":2000000,
   "architecture":{"output_modalities":["text"]},
   "pricing":{"prompt":"-1","completion":"-1"}}
]}`

func servidorDeModelos(t *testing.T, cuerpo string, codigo int) *Cliente {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("pidió %s", r.URL.Path)
		}
		w.WriteHeader(codigo)
		w.Write([]byte(cuerpo))
	}))
	t.Cleanup(s.Close)
	t.Cleanup(vaciarCache)
	vaciarCache()
	return NuevoCliente(Opciones{Base: s.URL})
}

func vaciarCache() {
	cache.mu.Lock()
	cache.lista, cache.traidos = nil, time.Time{}
	cache.mu.Unlock()
}

func TestLosModelosSalenOrdenadosYTraducidos(t *testing.T) {
	c := servidorDeModelos(t, catalogoDePrueba, http.StatusOK)
	ms, err := c.Modelos(context.Background(), "sk-or-v1-loquesea")
	if err != nil {
		t.Fatal(err)
	}
	// El que dibuja no sirve acá, y el enrutador cobra lo que cueste el que
	// elija: no se puede mostrar un precio que todavía no existe.
	if len(ms) != 2 {
		for _, m := range ms {
			t.Logf("quedó %s", m.ID)
		}
		t.Fatalf("quedaron %d modelos y tenían que quedar 2", len(ms))
	}
	if ms[0].ID != "anthropic/claude-3.5-haiku" {
		t.Errorf("el primero por nombre es %s", ms[0].ID)
	}
	// El precio viene por token y hay que poder compararlo: 0.0000008 por
	// token son 80 centavos por millón.
	if ms[0].PorMillonEntrada != 0.8 || ms[0].PorMillonSalida != 4 {
		t.Errorf("precios = %v / %v", ms[0].PorMillonEntrada, ms[0].PorMillonSalida)
	}
	if ms[0].Precio() != "US$0.80 / US$4 por millón" {
		t.Errorf("precio escrito = %q", ms[0].Precio())
	}
	if !ms[1].Gratis || ms[1].Precio() != "gratis" {
		t.Errorf("el gratis no quedó marcado: %+v", ms[1])
	}
}

func TestUnaClaveRechazadaAlPedirModelosSeDistingue(t *testing.T) {
	c := servidorDeModelos(t, `{"error":{}}`, http.StatusUnauthorized)
	if _, err := c.Modelos(context.Background(), "mala"); err != ErrClaveRechazada {
		t.Errorf("devolvió %v", err)
	}
}

// La lista es la misma para todo el mundo y son cientos de modelos: pedirla en
// cada carga de la página sería una vuelta a la red al pedo.
func TestLaListaSeGuardaUnRato(t *testing.T) {
	var veces int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		veces++
		w.Write([]byte(catalogoDePrueba))
	}))
	defer s.Close()
	vaciarCache()
	defer vaciarCache()

	c := NuevoCliente(Opciones{Base: s.URL})
	for i := 0; i < 3; i++ {
		if _, err := c.Modelos(context.Background(), "sk"); err != nil {
			t.Fatal(err)
		}
	}
	if veces != 1 {
		t.Errorf("le pidió la lista %d veces", veces)
	}
}
