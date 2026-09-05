//go:build red

package saij

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Cuánto cuesta tener el catálogo entero en memoria y buscarlo. El índice
// existe porque se supone que 81 mil normas entran holgadas; esto lo mide en
// vez de suponerlo.
func TestRedElIndiceEnteroEsBarato(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancelar()

	c := clienteReal(t)
	info, err := c.BuscarCatalogo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	destino := filepath.Join(t.TempDir(), "saij.csv")
	if err := c.DescargarCatalogo(ctx, info.URL, destino); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(destino)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var antes, despues runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&antes)

	i := NuevoIndice()
	empezo := time.Now()
	n, err := i.Cargar(f)
	tardo := time.Since(empezo)
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	runtime.ReadMemStats(&despues)
	mb := float64(despues.HeapAlloc-antes.HeapAlloc) / (1 << 20)

	t.Logf("normas: %d | carga: %s | memoria: %.1f MB", n, tardo.Round(time.Millisecond), mb)

	if tardo > 30*time.Second {
		t.Errorf("la carga tardó %s: demasiado para hacerla al arrancar", tardo)
	}
	// Si el catálogo creciera hasta no entrar, el índice en memoria deja de
	// ser la decisión correcta y hay que enterarse acá.
	if mb > 250 {
		t.Errorf("el índice ocupa %.0f MB; ya no entra holgado", mb)
	}

	// Y buscar sobre el catálogo entero tiene que ser instantáneo: es una
	// pasada lineal sobre 81 mil registros, no una base de datos.
	casos := []Consulta{
		{Texto: "educación"},
		{Texto: "presupuesto", Provincia: "Buenos Aires"},
		{Texto: "codigo procesal penal", SoloVigentes: true},
		{Provincia: "Salta", Desde: 2020},
		{}, // sin filtros, el peor caso
	}
	for _, q := range casos {
		empezo := time.Now()
		r := i.Buscar(q)
		tardo := time.Since(empezo)
		t.Logf("  %+v -> %d en %s", q, r.Total, tardo.Round(time.Microsecond))
		if tardo > 500*time.Millisecond {
			t.Errorf("la búsqueda %+v tardó %s", q, tardo)
		}
	}
}
