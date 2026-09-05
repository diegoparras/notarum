package asistente

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Cualquier modelo que el proveedor ofrezca tiene que poder usarse.
//
// No todos aceptan los mismos parámetros: la familia GPT-5 rechaza
// temperature, y mandárselo igual rompe la generación entera antes de empezar.
// El catálogo dice qué acepta cada uno, así que se le pregunta en vez de
// mantener una lista escrita acá que envejece sola.
func TestNoSeLeMandaAUnModeloUnParametroQueNoAcepta(t *testing.T) {
	const catalogo = `{"data":[
	  {"id":"openai/gpt-5.6-luna","name":"GPT-5.6 Luna","context_length":400000,
	   "architecture":{"output_modalities":["text"]},
	   "pricing":{"prompt":"0.000001","completion":"0.000008"},
	   "supported_parameters":["max_tokens","tools"]},
	  {"id":"anthropic/claude-haiku-4.5","name":"Claude Haiku 4.5","context_length":200000,
	   "architecture":{"output_modalities":["text"]},
	   "pricing":{"prompt":"0.000001","completion":"0.000005"},
	   "supported_parameters":["max_tokens","temperature","tools"]}
	]}`

	var enviado map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(catalogo))
			return
		}
		enviado = nil
		json.NewDecoder(r.Body).Decode(&enviado)
		w.Write([]byte(`{"choices":[{"message":{"content":"listo"}}],"usage":{}}`))
	}))
	defer s.Close()
	vaciarCache()
	defer vaciarCache()

	c := NuevoCliente(Opciones{Base: s.URL})

	// El que no la acepta: no se le manda, y genera con la suya.
	if _, err := c.Generar(context.Background(), "sk", "openai/gpt-5.6-luna", "sistema", "algo"); err != nil {
		t.Fatal(err)
	}
	if _, hay := enviado["temperature"]; hay {
		t.Error("se le mandó temperature a un modelo que no la acepta")
	}
	if _, hay := enviado["max_tokens"]; !hay {
		t.Error("no se le mandó max_tokens, que sí acepta")
	}

	// El que sí la acepta la recibe: la consulta tiene que salir igual todas
	// las veces, no variada.
	if _, err := c.Generar(context.Background(), "sk", "anthropic/claude-haiku-4.5", "sistema", "algo"); err != nil {
		t.Fatal(err)
	}
	if enviado["temperature"] != 0.1 {
		t.Errorf("temperature = %v", enviado["temperature"])
	}
}

// Y si el catálogo no se puede traer, no se manda ninguno: omitir un parámetro
// deja al proveedor con su valor por defecto, mandarle uno que no acepta rompe
// el pedido. Ante la duda, la que no rompe.
func TestSinCatalogoNoSeArriesgaNingunParametro(t *testing.T) {
	var enviado map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewDecoder(r.Body).Decode(&enviado)
		w.Write([]byte(`{"choices":[{"message":{"content":"listo"}}],"usage":{}}`))
	}))
	defer s.Close()
	vaciarCache()
	defer vaciarCache()

	c := NuevoCliente(Opciones{Base: s.URL})
	if _, err := c.Generar(context.Background(), "sk", "un/modelo", "sistema", "algo"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"temperature", "max_tokens"} {
		if _, hay := enviado[p]; hay {
			t.Errorf("se arriesgó %s sin saber si el modelo lo acepta", p)
		}
	}
}

// No se le pide a un modelo más de lo que puede escribir: van desde 4096 hasta
// cientos de miles, y pasarse es otro error evitable.
func TestElTechoSeAcotaAloQueElModeloAdmite(t *testing.T) {
	chico := Modelo{MaxSalida: 4096}
	if got := chico.TechoDeSalida(8000); got != 4096 {
		t.Errorf("con tope de 4096 pidió %d", got)
	}
	grande := Modelo{MaxSalida: 100000}
	if got := grande.TechoDeSalida(8000); got != 8000 {
		t.Errorf("con tope de 100000 pidió %d", got)
	}
	// El que no lo declara: se usa el nuestro, que es el que conocemos.
	callado := Modelo{}
	if got := callado.TechoDeSalida(8000); got != 8000 {
		t.Errorf("sin tope declarado pidió %d", got)
	}
}
