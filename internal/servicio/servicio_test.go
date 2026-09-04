package servicio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
)

func fixture(t *testing.T, nombre string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "boletin", "testdata", nombre))
	if err != nil {
		t.Fatalf("no se pudo leer el fixture %s: %v", nombre, err)
	}
	return b
}

func armar(t *testing.T, h http.Handler) (*Servicio, *int32) {
	t.Helper()
	var pedidos int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: srv.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
	})
	c, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Nuevo(cli, c), &pedidos
}

// Una edición pasada se lee del sitio una sola vez.
func TestEdicionPasadaSeLeeUnaSolaVez(t *testing.T) {
	cuerpo := fixture(t, "portada_primera_20260901.html")
	s, pedidos := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(cuerpo)
	}))
	fecha, _ := boletin.ParseFecha("2026-09-01")

	for i := 0; i < 3; i++ {
		ed, err := s.Edicion(context.Background(), boletin.Primera, fecha, "")
		if err != nil {
			t.Fatal(err)
		}
		if ed.Cantidad != 100 {
			t.Fatalf("cantidad = %d", ed.Cantidad)
		}
	}
	if *pedidos != 1 {
		t.Errorf("pedidos al sitio = %d, se esperaba 1", *pedidos)
	}
	if m := s.Almacen().Metricas(); m.Aciertos != 2 {
		t.Errorf("aciertos de caché = %d, se esperaban 2", m.Aciertos)
	}
}

// "No hubo edición" también se guarda: no tiene sentido volver a preguntar por
// un feriado de hace tres años.
func TestSinEdicionSeCachea(t *testing.T) {
	s, pedidos := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	fecha, _ := boletin.ParseFecha("2026-08-17")
	for i := 0; i < 3; i++ {
		if _, err := s.Edicion(context.Background(), boletin.Primera, fecha, ""); !errors.Is(err, ErrSinEdicion) {
			t.Fatalf("err = %v", err)
		}
	}
	if *pedidos != 1 {
		t.Errorf("pedidos = %d, se esperaba 1", *pedidos)
	}
}

// El filtro por rubro sale de la edición cacheada, sin pegarle otra vez al sitio.
func TestFiltroPorRubroNoPegaAlSitio(t *testing.T) {
	cuerpo := fixture(t, "portada_primera_20260901.html")
	s, pedidos := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(cuerpo)
	}))
	fecha, _ := boletin.ParseFecha("2026-09-01")

	completa, err := s.Edicion(context.Background(), boletin.Primera, fecha, "")
	if err != nil {
		t.Fatal(err)
	}
	soloDecretos, err := s.Edicion(context.Background(), boletin.Primera, fecha, "decretos")
	if err != nil {
		t.Fatal(err)
	}
	if *pedidos != 1 {
		t.Errorf("pedidos = %d: el filtro no debería pedir de nuevo", *pedidos)
	}
	if soloDecretos.Cantidad == 0 {
		t.Fatal("el filtro por DECRETOS no devolvió nada")
	}
	if soloDecretos.Cantidad >= completa.Cantidad {
		t.Errorf("filtrada = %d, completa = %d", soloDecretos.Cantidad, completa.Cantidad)
	}
	for _, a := range soloDecretos.Avisos {
		if a.Rubro != "DECRETOS" {
			t.Errorf("se coló el rubro %q", a.Rubro)
		}
	}
	if completa.Cantidad != 100 {
		t.Errorf("la edición completa quedó alterada: %d", completa.Cantidad)
	}
}

func TestResumenesMarcaLasFaltantes(t *testing.T) {
	cal := fixture(t, "calendario_primera_2026.json")
	portada := fixture(t, "portada_primera_20260901.html")
	s, _ := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/calendario/dias_publicacion/2026/primera" {
			w.Write(cal)
			return
		}
		w.Write(portada)
	}))
	desde, _ := boletin.ParseFecha("2026-09-01")
	hasta, _ := boletin.ParseFecha("2026-09-04")

	r, err := s.Resumenes(context.Background(), boletin.Primera, desde, hasta)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Ediciones) != 0 {
		t.Errorf("sin caché no debería haber resúmenes, hubo %d", len(r.Ediciones))
	}
	if len(r.Faltantes) == 0 {
		t.Fatal("no se marcó ninguna fecha faltante")
	}

	// Una vez bajada la del 1/9, deja de faltar.
	if _, err := s.Edicion(context.Background(), boletin.Primera, desde, ""); err != nil {
		t.Fatal(err)
	}
	r2, err := s.Resumenes(context.Background(), boletin.Primera, desde, hasta)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Ediciones) != 1 {
		t.Fatalf("resúmenes = %d, se esperaba 1", len(r2.Ediciones))
	}
	if r2.Ediciones[0].Cantidad != 100 || len(r2.Ediciones[0].PorRubro) == 0 {
		t.Errorf("resumen = %+v", r2.Ediciones[0])
	}
	if len(r2.Faltantes) != len(r.Faltantes)-1 {
		t.Errorf("faltantes = %d, antes eran %d", len(r2.Faltantes), len(r.Faltantes))
	}
}

func TestResumenesRechazaRangosImposibles(t *testing.T) {
	s, _ := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	a, _ := boletin.ParseFecha("2026-09-04")
	b, _ := boletin.ParseFecha("2026-09-01")
	if _, err := s.Resumenes(context.Background(), boletin.Primera, a, b); err == nil {
		t.Error("se aceptó un rango al revés")
	}
	largo, _ := boletin.ParseFecha("2028-01-01")
	if _, err := s.Resumenes(context.Background(), boletin.Primera, b, largo); err == nil {
		t.Error("se aceptó un rango de más de 366 días")
	}
}

func TestAvisoSeCachea(t *testing.T) {
	cuerpo := fixture(t, "detalle_primera_346633.html")
	s, pedidos := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(cuerpo)
	}))
	fecha, _ := boletin.ParseFecha("2026-09-01")
	for i := 0; i < 2; i++ {
		d, err := s.Aviso(context.Background(), boletin.Primera, "346633", fecha)
		if err != nil {
			t.Fatal(err)
		}
		if d.Organismo != "PODER EJECUTIVO" || d.Texto == "" {
			t.Fatalf("detalle = %+v", d.Aviso)
		}
	}
	if *pedidos != 1 {
		t.Errorf("pedidos = %d, se esperaba 1", *pedidos)
	}
}

func TestRubrosSeCachean(t *testing.T) {
	cuerpo := fixture(t, "rubros_primera.json")
	s, pedidos := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(cuerpo)
	}))
	for i := 0; i < 2; i++ {
		rs, err := s.Rubros(context.Background(), boletin.Primera)
		if err != nil {
			t.Fatal(err)
		}
		if len(rs) < 5 {
			t.Fatalf("rubros = %d", len(rs))
		}
	}
	if *pedidos != 1 {
		t.Errorf("pedidos = %d, se esperaba 1", *pedidos)
	}
}

func TestAnexoSeCacheaYVuelvePDF(t *testing.T) {
	s, pedidos := armar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"pdfBase64":"JVBERi0xLjQ="}`))
	}))
	fecha, _ := boletin.ParseFecha("2026-09-01")
	for i := 0; i < 2; i++ {
		pdf, err := s.Anexo(context.Background(), boletin.Primera, "12", "7756488", fecha)
		if err != nil {
			t.Fatal(err)
		}
		if string(pdf) != "%PDF-1.4" {
			t.Fatalf("pdf = %q", pdf)
		}
	}
	if *pedidos != 1 {
		t.Errorf("pedidos = %d, se esperaba 1", *pedidos)
	}
}

func TestTTLPara(t *testing.T) {
	ayer, _ := boletin.ParseFecha("2020-01-02")
	if ttlPara(ayer) != almacen.SinVencimiento {
		t.Error("una edición pasada no debería vencer")
	}
	if ttlPara(boletin.HoyEnArgentina()) != TTLHoy {
		t.Error("la edición de hoy debería vencer a los 5 minutos")
	}
}
