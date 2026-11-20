package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

// Un modelo que usa las herramientas tiene que poder enterarse de que el
// Boletín está limitando, y hasta cuándo, sin adivinarlo por los errores.
func TestEstadoDiceQueElSitioEstaLimitando(t *testing.T) {
	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(origen.Close)
	cli := boletin.NuevoCliente(boletin.Opciones{Base: origen.URL, Intervalo: time.Millisecond})
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { alm.Cerrar() })
	s := Nuevo(servicio.Nuevo(cli, alm), "test")

	type estado struct {
		SitioLimitando     bool       `json:"sitio_limitando"`
		SitioLimitadoHasta *time.Time `json:"sitio_limitado_hasta"`
	}
	var antes estado
	decodificar(t, llamarHerramienta(t, s, "estado", map[string]any{}), &antes)
	if antes.SitioLimitando || antes.SitioLimitadoHasta != nil {
		t.Errorf("antes de cualquier 429 el estado habla de límite: %+v", antes)
	}

	r := llamarHerramienta(t, s, "rubros", map[string]any{"seccion": "primera"})
	if !r.EsError {
		t.Fatal("con el sitio contestando 429 la herramienta tenía que devolver error")
	}
	if texto := r.Contenido[0].Texto; !strings.Contains(texto, "respondió 429") {
		t.Errorf("el error no explica qué pasó: %s", texto)
	}

	var despues estado
	decodificar(t, llamarHerramienta(t, s, "estado", map[string]any{}), &despues)
	if !despues.SitioLimitando {
		t.Error("sitio_limitando = false después de un 429")
	}
	if despues.SitioLimitadoHasta == nil || !despues.SitioLimitadoHasta.After(time.Now()) {
		t.Errorf("sitio_limitado_hasta = %v, se esperaba una hora futura", despues.SitioLimitadoHasta)
	}
}
