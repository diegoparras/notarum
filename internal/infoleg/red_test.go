//go:build red

// Estos tests pegan a InfoLEG y al portal de datos reales.
//
//	go test ./internal/infoleg/ -tags red -v
//
// No corren en la suite normal. Sirven para enterarse de que el sitio o la
// publicación del catálogo cambiaron, antes de que lo note quien consume.
package infoleg

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func clienteReal(t *testing.T) *Cliente {
	t.Helper()
	return NuevoCliente(Opciones{
		UserAgent: "notarum-test/1.1 (+https://github.com/diegoparras/notarum)",
		Intervalo: time.Second, // más lento que en producción: es una prueba
	})
}

// La ruta calculada tiene que devolver el texto de la Ley de Bases, que es el
// caso con el que se descubrió el truco.
func TestRedTextoDeUnaLeyConocida(t *testing.T) {
	c := clienteReal(t)
	ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
	defer cancelar()

	texto, err := c.TraerTexto(ctx, 401266) // Ley 27.742
	if err != nil {
		t.Fatalf("no se pudo leer la norma: %v", err)
	}
	if len(texto.Texto) < 10000 {
		t.Errorf("el texto quedó en %d caracteres: se esperaba la ley entera", len(texto.Texto))
	}
	// Los acentos tienen que haber sobrevivido a la conversión de codificación.
	for _, esperado := range []string{
		"LEY DE BASES Y PUNTOS DE PARTIDA PARA LA LIBERTAD DE LOS ARGENTINOS",
		"Cámara de Diputados",
	} {
		if !strings.Contains(texto.Texto, esperado) {
			t.Errorf("no aparece %q en el texto", esperado)
		}
	}
	if strings.Contains(texto.Texto, "Ã") || strings.Contains(texto.Texto, "�") {
		t.Error("hay acentos rotos: falló la conversión de ISO-8859-1")
	}
	if strings.Contains(texto.HTML, "<script") || strings.Contains(texto.HTML, "<style") {
		t.Error("el HTML no quedó saneado")
	}
}

// Un decreto que el catálogo lista sin texto_original no tiene archivo: el
// sitio redirige y eso tiene que llegar como ErrSinTexto.
func TestRedNormaSinTextoPublicado(t *testing.T) {
	c := clienteReal(t)
	_, err := c.TraerTexto(context.Background(), 429014) // Decreto 759/2026
	if !errors.Is(err, ErrSinTexto) {
		t.Fatalf("err = %v, se esperaba ErrSinTexto", err)
	}
}

// El portal de datos tiene que seguir publicando el catálogo donde se espera.
func TestRedBuscarCatalogo(t *testing.T) {
	c := clienteReal(t)
	ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
	defer cancelar()

	info, err := c.BuscarCatalogo(ctx)
	if err != nil {
		t.Fatalf("no se encontró el catálogo: %v", err)
	}
	if !strings.HasSuffix(strings.ToLower(info.URL), ".zip") && !strings.Contains(info.URL, "resource") {
		t.Errorf("url = %q", info.URL)
	}
	if info.Actualizado.IsZero() {
		t.Error("el catálogo no informa cuándo se actualizó")
	}
	// Se publica a diario: si hace más de un mes que no se toca, algo pasó.
	if time.Since(info.Actualizado) > 30*24*time.Hour {
		t.Errorf("el catálogo no se actualiza desde %v", info.Actualizado)
	}
	t.Logf("catálogo: %s (%.1f MB, actualizado %s)",
		info.URL, float64(info.Bytes)/1e6, info.Actualizado.Format("2006-01-02"))
}
