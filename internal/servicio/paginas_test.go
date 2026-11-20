package servicio

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
)

// guardarCortada deja en la caché la Primera del 1/9/2026 como la guardaba
// notarum hasta la 1.7.2: la portada sola, 100 avisos, para siempre.
func guardarCortada(t *testing.T, s *Servicio) boletin.Fecha {
	t.Helper()
	fecha, _ := boletin.ParseFecha("2026-09-01")
	ed, err := boletin.ParsearPortada(fixture(t, "portada_primera_20260901.html"), boletin.Primera, fecha)
	if err != nil {
		t.Fatal(err)
	}
	// Lo que no existía entonces.
	ed.Paginas, ed.Declarados, ed.Parcial = 0, 0, false
	crudo, err := json.Marshal(ed)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Almacen().Guardar(claveEdicion(boletin.Primera, fecha), crudo, almacen.SinVencimiento); err != nil {
		t.Fatal(err)
	}
	return fecha
}

// Las ediciones que ya estaban guardadas cortadas se completan solas la
// primera vez que alguien las pide: nadie tiene que borrar nada a mano.
func TestEdicionCortadaDeAntesSeVuelveALeer(t *testing.T) {
	s, pedidos := armar(t, primera20260901(t))
	fecha := guardarCortada(t, s)

	if s.TieneEdicionEnCache(boletin.Primera, fecha) {
		t.Error("una edición cortada no puede contar como bajada: el relleno la saltearía")
	}

	ed, err := s.Edicion(context.Background(), boletin.Primera, fecha, "")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 129 || ed.Parcial {
		t.Fatalf("cantidad = %d, parcial = %v; se esperaban los 129", ed.Cantidad, ed.Parcial)
	}
	if *pedidos != 2 {
		t.Errorf("pedidos = %d, se esperaban 2", *pedidos)
	}

	// Ya completa, no se vuelve a pedir.
	if _, err := s.Edicion(context.Background(), boletin.Primera, fecha, ""); err != nil {
		t.Fatal(err)
	}
	if *pedidos != 2 {
		t.Errorf("pedidos = %d: la edición completa se volvió a pedir", *pedidos)
	}
	if !s.TieneEdicionEnCache(boletin.Primera, fecha) {
		t.Error("la edición completa no figura como bajada")
	}
}

// Si el sitio no contesta, la cortada se sirve igual, pero dice que lo es.
func TestEdicionCortadaSeSirveParcialSiElSitioNoContesta(t *testing.T) {
	s, _ := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	fecha := guardarCortada(t, s)

	ed, err := s.Edicion(context.Background(), boletin.Primera, fecha, "")
	if err != nil {
		t.Fatalf("se esperaba la edición guardada, vino el error: %v", err)
	}
	if ed.Cantidad != 100 || !ed.Parcial {
		t.Errorf("cantidad = %d, parcial = %v; se esperaba 100 y parcial", ed.Cantidad, ed.Parcial)
	}
	// El recorte por rubro hereda el aviso.
	decretos, err := s.Edicion(context.Background(), boletin.Primera, fecha, "decretos")
	if err != nil {
		t.Fatal(err)
	}
	if !decretos.Parcial {
		t.Error("el recorte por rubro de una edición parcial no lo dice")
	}
}

// Un rango no le pide nada al sitio, así que no completa: avisa.
func TestResumenesAvisaLasCortadas(t *testing.T) {
	cal := fixture(t, "calendario_primera_2026.json")
	s, _ := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(cal)
	}))
	fecha := guardarCortada(t, s)

	r, err := s.Resumenes(context.Background(), boletin.Primera, fecha, fecha)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Ediciones) != 1 || !r.Ediciones[0].Parcial {
		t.Errorf("resúmenes = %+v: la edición cortada tiene que salir marcada", r.Ediciones)
	}
}

// Una edición parcial se vuelve a intentar: no queda guardada para siempre
// aunque sea de una fecha pasada.
func TestEdicionParcialNoSeGuardaParaSiempre(t *testing.T) {
	fecha, _ := boletin.ParseFecha("2026-09-01")
	completa := &boletin.Edicion{Fecha: fecha}
	if ttl := ttlDeEdicion(completa); ttl != almacen.SinVencimiento {
		t.Errorf("ttl de una edición pasada y completa = %v", ttl)
	}
	parcial := &boletin.Edicion{Fecha: fecha, Parcial: true}
	if ttl := ttlDeEdicion(parcial); ttl != TTLHoy {
		t.Errorf("ttl de una edición parcial = %v, se esperaba %v", ttl, TTLHoy)
	}
}
