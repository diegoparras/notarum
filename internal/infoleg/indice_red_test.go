//go:build red

package infoleg

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Cuánto cuesta tener las 428 mil normas nacionales buscables. El índice
// recorta los campos a propósito; esto mide si alcanzó.
func TestRedElIndiceNacionalEntra(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancelar()

	c := clienteReal(t)
	info, err := c.BuscarCatalogo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rutaZip := filepath.Join(dir, "catalogo.zip")
	if err := c.DescargarCatalogo(ctx, info.URL, rutaZip); err != nil {
		t.Fatal(err)
	}
	lector, err := AbrirCatalogo(rutaZip)
	if err != nil {
		t.Fatal(err)
	}
	defer lector.Close()

	var antes, despues runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&antes)

	i := NuevoIndiceCon(NormasEsperadas)
	internar := Internador()
	empezo := time.Now()
	leidas, err := LeerCatalogo(lector, func(n Norma) error {
		i.Agregar(n, internar)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	i.Cerrar()
	tardo := time.Since(empezo)

	runtime.GC()
	runtime.ReadMemStats(&despues)
	mb := float64(despues.HeapAlloc-antes.HeapAlloc) / (1 << 20)
	t.Logf("normas: %d | armado: %s | memoria: %.0f MB", leidas, tardo.Round(time.Second), mb)

	if i.Normas() < 400000 {
		t.Errorf("sólo entraron %d normas", i.Normas())
	}
	// El techo: más que esto y el buscador nacional deja de ser algo que se
	// pueda tener prendido siempre en un contenedor común.
	if mb > 400 {
		t.Errorf("el índice ocupa %.0f MB", mb)
	}

	for _, q := range []Consulta{
		{Texto: "defensa del consumidor"},
		{Texto: "ley 24240"},
		{Tipo: "Decreto", Desde: 2020},
		{Texto: "educacion", SoloConTexto: true},
	} {
		empezo := time.Now()
		r := i.Buscar(q)
		tardo := time.Since(empezo)
		t.Logf("  %+v -> %d en %s", q, r.Total, tardo.Round(time.Millisecond))
		if tardo > 2*time.Second {
			t.Errorf("la búsqueda %+v tardó %s", q, tardo)
		}
		if len(r.Normas) > 0 {
			t.Logf("     primera: %s %s (%s) — %.60s", r.Normas[0].Tipo, r.Normas[0].Numero,
				r.Normas[0].Fecha, r.Normas[0].Titulo)
		}
	}
	_ = os.Remove(rutaZip)
}
