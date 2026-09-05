//go:build red

package infoleg

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// De dónde salen los megabytes del índice nacional. Sin esto, optimizar es
// adivinar.
func TestRedDesgloseDelIndice(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancelar()

	c := clienteReal(t)
	info, err := c.BuscarCatalogo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rutaZip := filepath.Join(t.TempDir(), "catalogo.zip")
	if err := c.DescargarCatalogo(ctx, info.URL, rutaZip); err != nil {
		t.Fatal(err)
	}
	lector, err := AbrirCatalogo(rutaZip)
	if err != nil {
		t.Fatal(err)
	}
	defer lector.Close()

	var bytesDe = map[string]int{}
	var n int
	i := NuevoIndiceCon(NormasEsperadas)
	internar := Internador()
	LeerCatalogo(lector, func(x Norma) error {
		n++
		bytesDe["buscado"] += len(textoDe(x))
		bytesDe["titulo"] += len(x.TituloResumido)
		bytesDe["numero"] += len(x.Numero)
		bytesDe["fecha"] += len(x.FechaSancion)
		bytesDe["tipo (sin internar)"] += len(x.Tipo)
		i.Agregar(x, internar)
		return nil
	})
	i.Cerrar()

	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)

	t.Logf("normas: %d", n)
	for k, v := range bytesDe {
		t.Logf("  %-22s %6.1f MB", k, float64(v)/(1<<20))
	}
	// Cada string en Go son 16 bytes de cabecera además de sus datos.
	t.Logf("  %-22s %6.1f MB", "cabeceras (5 strings)", float64(n*5*16)/(1<<20))
	t.Logf("  %-22s %6.1f MB", "el resto de EnIndice", float64(n*(4+2+1))/(1<<20))
	t.Logf("  %-22s %6.1f MB", "mapa porID", float64(n*24)/(1<<20))
	t.Logf("heap: %.0f MB | proceso: %.0f MB", float64(m.HeapAlloc)/(1<<20), float64(m.Sys)/(1<<20))
}
