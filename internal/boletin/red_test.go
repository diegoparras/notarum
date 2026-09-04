//go:build red

// Estos tests pegan al sitio real del Boletín Oficial. No corren en la suite
// normal: hay que pedirlos explícitamente.
//
//	go test ./internal/boletin/ -tags red -v
//
// Sirven para enterarse de que el sitio cambió de forma antes de que lo note
// quien consume la API.
package boletin

import (
	"context"
	"errors"
	"testing"
	"time"
)

func clienteReal(t *testing.T) *Cliente {
	t.Helper()
	return NuevoCliente(Opciones{
		UserAgent: "notarum-test/1.0 (+https://github.com/diegoparras/notarum)",
		Intervalo: time.Second, // más lento que en producción: es una prueba
	})
}

// Las cantidades son las que devolvió el sitio el 4/9/2026. Una edición pasada
// no cambia: si estos números cambian, cambió la extracción o cambió el sitio.
func TestRedCantidadesConocidas(t *testing.T) {
	c := clienteReal(t)
	casos := []struct {
		seccion  Seccion
		fecha    string
		cantidad int
	}{
		{Primera, "2026-07-15", 73},
		{Primera, "2025-03-10", 52},
		{Primera, "2026-09-01", 100},
		{Segunda, "2026-09-01", 100},
		{Tercera, "2026-09-01", 54},
	}
	ctx, cancelar := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelar()

	for _, caso := range casos {
		t.Run(string(caso.seccion)+"/"+caso.fecha, func(t *testing.T) {
			fecha, err := ParseFecha(caso.fecha)
			if err != nil {
				t.Fatal(err)
			}
			ed, err := c.TraerEdicion(ctx, caso.seccion, fecha, "")
			if err != nil {
				t.Fatalf("no se pudo leer la edición: %v", err)
			}
			if ed.Cantidad != caso.cantidad {
				t.Errorf("cantidad = %d, se esperaba %d: el sitio o el parser cambiaron",
					ed.Cantidad, caso.cantidad)
			}
			// El organismo puede venir vacío y es legítimo: en el rubro
			// LEYES, los decretos de promulgación no lo traen.
			for _, a := range ed.Avisos {
				if a.ID == "" || a.Rubro == "" || a.URL == "" {
					t.Errorf("aviso incompleto: %+v", a)
					break
				}
			}
		})
	}
}

// Un feriado no tiene edición: el sitio contesta 302.
func TestRedFeriadoNoTieneEdicion(t *testing.T) {
	c := clienteReal(t)
	fecha, _ := ParseFecha("2026-08-17")
	_, err := c.TraerEdicion(context.Background(), Primera, fecha, "")
	if !errors.Is(err, ErrSinEdicion) {
		t.Fatalf("err = %v, se esperaba ErrSinEdicion", err)
	}
}

func TestRedDetalleYAnexos(t *testing.T) {
	c := clienteReal(t)
	fecha, _ := ParseFecha("2026-09-01")
	d, err := c.TraerAviso(context.Background(), Primera, "346633", fecha)
	if err != nil {
		t.Fatal(err)
	}
	if d.Organismo != "PODER EJECUTIVO" || d.Norma != "Decreto 845/2026" {
		t.Errorf("cabecera = %q / %q", d.Organismo, d.Norma)
	}
	if d.FechaPublicacion != "2026-09-01" {
		t.Errorf("fecha_publicacion = %q", d.FechaPublicacion)
	}
	if len(d.Anexos) != 12 {
		t.Errorf("anexos = %d, se esperaban 12", len(d.Anexos))
	}
	if len(d.Anexos) > 0 {
		pdf, err := c.TraerAnexo(context.Background(), Primera, d.Anexos[0].Numero, d.Anexos[0].ID, fecha)
		if err != nil {
			t.Fatalf("no se pudo bajar el anexo: %v", err)
		}
		if len(pdf) < 1000 || string(pdf[:4]) != "%PDF" {
			t.Errorf("el anexo no es un PDF (%d bytes)", len(pdf))
		}
	}
}

func TestRedCalendarioYRubros(t *testing.T) {
	c := clienteReal(t)
	cal, err := c.TraerCalendario(context.Background(), Primera, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Fechas) < 100 {
		t.Errorf("fechas = %d", len(cal.Fechas))
	}
	for _, f := range cal.Fechas {
		if f.API() == "2026-08-17" {
			t.Error("el 17/8/2026 es feriado y aparece en el calendario")
		}
	}

	for _, sec := range SeccionesValidas {
		rs, err := c.TraerRubros(context.Background(), sec)
		if err != nil {
			t.Errorf("rubros de %s: %v", sec, err)
			continue
		}
		if len(rs) == 0 {
			t.Errorf("la sección %s vino sin rubros", sec)
		}
	}
}

func TestRedBusqueda(t *testing.T) {
	c := clienteReal(t)
	desde, _ := ParseFecha("2026-09-01")
	hasta, _ := ParseFecha("2026-09-03")
	res, err := c.Buscar(context.Background(), ConsultaBusqueda{
		Texto: "decreto", Seccion: Primera, Desde: desde, Hasta: hasta,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cantidad == 0 {
		t.Error("la búsqueda no devolvió nada")
	}
	for _, a := range res.Avisos {
		if a.ID == "" || a.Fecha.API() < "2026-09-01" || a.Fecha.API() > "2026-09-03" {
			t.Errorf("aviso fuera del rango pedido: %+v", a)
			break
		}
	}
}
