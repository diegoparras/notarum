package almacen

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
)

func nuevaSQLite(t *testing.T) *SQLite {
	t.Helper()
	s, err := NuevoSQLite(filepath.Join(t.TempDir(), "notarum.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Cerrar() })
	return s
}

// El almacén de SQLite tiene que comportarse igual que el de disco: es la
// misma interfaz con otro motor detrás.
func TestSQLiteCumpleLaMismaInterfaz(t *testing.T) {
	var _ Almacen = (*Disco)(nil)
	var _ Almacen = (*SQLite)(nil)
	var _ Indexador = (*SQLite)(nil)

	s := nuevaSQLite(t)
	if _, ok := s.Leer("nada"); ok {
		t.Error("un almacén vacío devolvió un acierto")
	}
	if err := s.Guardar("ediciones/primera/2026-09-01", []byte(`{"a":1}`), SinVencimiento); err != nil {
		t.Fatal(err)
	}
	datos, ok := s.Leer("ediciones/primera/2026-09-01")
	if !ok || string(datos) != `{"a":1}` {
		t.Errorf("datos = %s, ok = %v", datos, ok)
	}
	if !s.Existe("ediciones/primera/2026-09-01") {
		t.Error("Existe devolvió false")
	}
	if err := s.Borrar("ediciones/primera/2026-09-01"); err != nil {
		t.Fatal(err)
	}
	if s.Existe("ediciones/primera/2026-09-01") {
		t.Error("la entrada sigue después de borrarla")
	}
}

func TestSQLiteVencimiento(t *testing.T) {
	s := nuevaSQLite(t)
	if err := s.Guardar("hoy", []byte(`1`), 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, ok := s.Leer("hoy"); ok {
		t.Error("se devolvió una entrada vencida")
	}
	if err := s.Guardar("pasada", []byte(`1`), SinVencimiento); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, ok := s.Leer("pasada"); !ok {
		t.Error("una entrada sin vencimiento caducó")
	}
}

func TestSQLiteGuardarPisaLoAnterior(t *testing.T) {
	s := nuevaSQLite(t)
	_ = s.Guardar("x", []byte(`"viejo"`), SinVencimiento)
	_ = s.Guardar("x", []byte(`"nuevo"`), SinVencimiento)
	datos, _ := s.Leer("x")
	if string(datos) != `"nuevo"` {
		t.Errorf("datos = %s", datos)
	}
}

func edicionDePrueba(t *testing.T) *boletin.Edicion {
	t.Helper()
	f1, _ := boletin.ParseFecha("2026-09-01")
	return &boletin.Edicion{
		Seccion: boletin.Primera, Fecha: f1, Cantidad: 3,
		PorRubro: map[string]int{"DECRETOS": 2, "RESOLUCIONES": 1},
		Avisos: []boletin.Aviso{
			{ID: "1", Seccion: boletin.Primera, Fecha: f1, Rubro: "DECRETOS",
				Organismo: "PODER EJECUTIVO", Norma: "Decreto 845/2026",
				Referencia: "DECTO-2026-845-APN-PTE", Sintesis: "Promúlgase la Ley de Ministerios.",
				TieneAnexos: true, URL: "https://x/1"},
			{ID: "2", Seccion: boletin.Primera, Fecha: f1, Rubro: "DECRETOS",
				Organismo: "MINISTERIO DE SALUD", Norma: "Decreto 846/2026",
				Sintesis: "Designaciones en el hospital.", URL: "https://x/2"},
			{ID: "3", Seccion: boletin.Primera, Fecha: f1, Rubro: "RESOLUCIONES",
				Organismo: "ADUANA", Norma: "Resolución 12/2026",
				Sintesis: "Aranceles de importación.", URL: "https://x/3"},
		},
	}
}

func TestIndexarYBuscarLocal(t *testing.T) {
	s := nuevaSQLite(t)
	if err := s.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")

	casos := []struct {
		nombre   string
		q        ConsultaLocal
		esperado int
	}{
		{"por organismo", ConsultaLocal{Texto: "aduana"}, 1},
		{"por norma", ConsultaLocal{Texto: "decreto"}, 2},
		{"por sintesis", ConsultaLocal{Texto: "designaciones"}, 1},
		{"por referencia", ConsultaLocal{Texto: "APN-PTE"}, 1},
		{"dos palabras", ConsultaLocal{Texto: "ministerio salud"}, 1},
		{"sin resultados", ConsultaLocal{Texto: "petroleo"}, 0},
		{"filtrado por rubro", ConsultaLocal{Rubro: "DECRETOS"}, 2},
		{"texto y rubro", ConsultaLocal{Texto: "decreto", Rubro: "RESOLUCIONES"}, 0},
		{"todo el rango", ConsultaLocal{}, 3},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			c.q.Desde, c.q.Hasta = desde, hasta
			res, err := s.BuscarLocal(c.q)
			if err != nil {
				t.Fatal(err)
			}
			if res.Total != c.esperado {
				t.Errorf("total = %d, se esperaban %d", res.Total, c.esperado)
			}
			if len(res.Avisos) != c.esperado {
				t.Errorf("avisos = %d, se esperaban %d", len(res.Avisos), c.esperado)
			}
		})
	}
}

// Buscar en castellano tiene que funcionar con y sin acentos: nadie escribe
// "Promúlgase" en una caja de búsqueda.
func TestBuscarLocalIgnoraAcentos(t *testing.T) {
	s := nuevaSQLite(t)
	if err := s.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")
	for _, texto := range []string{"promúlgase", "promulgase", "PROMULGASE", "Promúlgase"} {
		res, err := s.BuscarLocal(ConsultaLocal{Texto: texto, Desde: desde, Hasta: hasta})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 1 {
			t.Errorf("buscando %q: total = %d, se esperaba 1", texto, res.Total)
		}
	}
}

// La búsqueda no puede romperse ni inyectar por lo que escriba el usuario.
func TestBuscarLocalToleraTextoRaro(t *testing.T) {
	s := nuevaSQLite(t)
	if err := s.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")
	raros := []string{`"`, `foo"bar`, `*`, `AND`, `OR NOT`, `a" OR "b`, `'; DROP TABLE avisos;--`, `()`, `^`, `NEAR(`}
	for _, texto := range raros {
		if _, err := s.BuscarLocal(ConsultaLocal{Texto: texto, Desde: desde, Hasta: hasta}); err != nil {
			t.Errorf("buscando %q devolvió error: %v", texto, err)
		}
	}
	// Y la tabla sigue en pie.
	res, err := s.BuscarLocal(ConsultaLocal{Desde: desde, Hasta: hasta})
	if err != nil || res.Total != 3 {
		t.Errorf("después de los textos raros: total = %d, err = %v", res.Total, err)
	}
}

func TestBuscarLocalFiltraPorSeccionYFecha(t *testing.T) {
	s := nuevaSQLite(t)
	if err := s.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")

	res, _ := s.BuscarLocal(ConsultaLocal{Seccion: boletin.Segunda, Desde: desde, Hasta: hasta})
	if res.Total != 0 {
		t.Errorf("la segunda no tiene nada indexado y devolvió %d", res.Total)
	}
	fuera1, _ := boletin.ParseFecha("2026-10-01")
	fuera2, _ := boletin.ParseFecha("2026-10-31")
	res, _ = s.BuscarLocal(ConsultaLocal{Desde: fuera1, Hasta: fuera2})
	if res.Total != 0 {
		t.Errorf("fuera del rango devolvió %d", res.Total)
	}
}

func TestBuscarLocalPagina(t *testing.T) {
	s := nuevaSQLite(t)
	if err := s.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")

	p1, err := s.BuscarLocal(ConsultaLocal{Desde: desde, Hasta: hasta, Limite: 2})
	if err != nil {
		t.Fatal(err)
	}
	if p1.Total != 3 || len(p1.Avisos) != 2 {
		t.Errorf("página 1: total = %d, avisos = %d", p1.Total, len(p1.Avisos))
	}
	p2, err := s.BuscarLocal(ConsultaLocal{Desde: desde, Hasta: hasta, Limite: 2, Desplazamiento: 2})
	if err != nil {
		t.Fatal(err)
	}
	if p2.Total != 3 || len(p2.Avisos) != 1 {
		t.Errorf("página 2: total = %d, avisos = %d", p2.Total, len(p2.Avisos))
	}
	if p1.Avisos[0].ID == p2.Avisos[0].ID {
		t.Error("las dos páginas devolvieron el mismo aviso")
	}
}

// Reindexar la misma edición no puede duplicar avisos.
func TestIndexarDosVecesNoDuplica(t *testing.T) {
	s := nuevaSQLite(t)
	ed := edicionDePrueba(t)
	for i := 0; i < 3; i++ {
		if err := s.IndexarEdicion(ed); err != nil {
			t.Fatal(err)
		}
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")
	res, _ := s.BuscarLocal(ConsultaLocal{Desde: desde, Hasta: hasta})
	if res.Total != 3 {
		t.Errorf("total = %d, se esperaban 3: se duplicaron avisos", res.Total)
	}
}

// El aviso que vuelve de la búsqueda tiene que estar completo.
func TestBuscarLocalDevuelveElAvisoEntero(t *testing.T) {
	s := nuevaSQLite(t)
	if err := s.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")
	res, err := s.BuscarLocal(ConsultaLocal{Texto: "aduana", Desde: desde, Hasta: hasta})
	if err != nil || len(res.Avisos) != 1 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	a := res.Avisos[0]
	if a.ID != "3" || a.Seccion != boletin.Primera || a.Fecha.API() != "2026-09-01" {
		t.Errorf("aviso = %+v", a)
	}
	if a.Rubro != "RESOLUCIONES" || a.Organismo != "ADUANA" || a.Norma != "Resolución 12/2026" {
		t.Errorf("aviso = %+v", a)
	}
	if a.URL != "https://x/3" {
		t.Errorf("url = %q", a.URL)
	}
}

func TestCobertura(t *testing.T) {
	s := nuevaSQLite(t)
	desde, _ := boletin.ParseFecha("2026-09-01")
	hasta, _ := boletin.ParseFecha("2026-09-30")

	n, err := s.Cobertura(boletin.Primera, desde, hasta)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sin indexar, cobertura = %d", n)
	}
	if err := s.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	n, err = s.Cobertura(boletin.Primera, desde, hasta)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("cobertura = %d, se esperaba 1 día indexado", n)
	}
}

func TestSQLiteMetricas(t *testing.T) {
	s := nuevaSQLite(t)
	s.Leer("no-esta")
	_ = s.Guardar("esta", []byte(`1`), SinVencimiento)
	s.Leer("esta")
	_ = s.IndexarEdicion(edicionDePrueba(t))

	m := s.Metricas()
	if m.Motor != "sqlite" {
		t.Errorf("motor = %q", m.Motor)
	}
	if m.Aciertos != 1 || m.Fallos != 1 || m.Escritos != 1 {
		t.Errorf("metricas = %+v", m)
	}
	if m.Entradas != 1 {
		t.Errorf("entradas = %d", m.Entradas)
	}
	if m.Avisos != 3 {
		t.Errorf("avisos indexados = %d, se esperaban 3", m.Avisos)
	}
}

// Los datos tienen que sobrevivir a cerrar y volver a abrir el archivo.
func TestSQLitePersisteEntreAperturas(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "notarum.db")
	s1, err := NuevoSQLite(ruta)
	if err != nil {
		t.Fatal(err)
	}
	_ = s1.Guardar("clave", []byte(`"valor"`), SinVencimiento)
	_ = s1.IndexarEdicion(edicionDePrueba(t))
	if err := s1.Cerrar(); err != nil {
		t.Fatal(err)
	}

	s2, err := NuevoSQLite(ruta)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Cerrar()
	datos, ok := s2.Leer("clave")
	if !ok || string(datos) != `"valor"` {
		t.Errorf("datos = %s, ok = %v", datos, ok)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")
	res, _ := s2.BuscarLocal(ConsultaLocal{Texto: "aduana", Desde: desde, Hasta: hasta})
	if res.Total != 1 {
		t.Errorf("el índice no sobrevivió al reinicio: total = %d", res.Total)
	}
}

// El texto completo del aviso tiene que entrar al índice, para que la búsqueda
// local llegue al cuerpo y no sólo al sumario.
func TestIndexarDetalleAgregaElCuerpo(t *testing.T) {
	s := nuevaSQLite(t)
	if err := s.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")

	// "hidrocarburos" está sólo en el cuerpo, no en el sumario.
	if res, _ := s.BuscarLocal(ConsultaLocal{Texto: "hidrocarburos", Desde: desde, Hasta: hasta}); res.Total != 0 {
		t.Fatalf("antes de indexar el cuerpo ya encontraba %d", res.Total)
	}

	f, _ := boletin.ParseFecha("2026-09-01")
	d := &boletin.Detalle{
		Aviso: boletin.Aviso{ID: "1", Seccion: boletin.Primera, Fecha: f},
		Texto: "VISTO el régimen de hidrocarburos y la promoción de inversiones.",
	}
	if err := s.IndexarDetalle(d); err != nil {
		t.Fatal(err)
	}
	res, err := s.BuscarLocal(ConsultaLocal{Texto: "hidrocarburos", Desde: desde, Hasta: hasta})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Errorf("total = %d, se esperaba 1", res.Total)
	}
	if len(res.Avisos) == 1 && res.Avisos[0].Organismo != "PODER EJECUTIVO" {
		t.Errorf("indexar el cuerpo pisó el resto del aviso: %+v", res.Avisos[0])
	}
	// Y lo del sumario tiene que seguir encontrándose.
	if r2, _ := s.BuscarLocal(ConsultaLocal{Texto: "promúlgase", Desde: desde, Hasta: hasta}); r2.Total != 1 {
		t.Errorf("se perdió la búsqueda por sumario: %d", r2.Total)
	}
}

// Reindexar la edición no puede borrar el texto que ya costó bajar.
func TestReindexarConservaElTexto(t *testing.T) {
	s := nuevaSQLite(t)
	_ = s.IndexarEdicion(edicionDePrueba(t))
	f, _ := boletin.ParseFecha("2026-09-01")
	_ = s.IndexarDetalle(&boletin.Detalle{
		Aviso: boletin.Aviso{ID: "1", Seccion: boletin.Primera, Fecha: f},
		Texto: "VISTO el régimen de hidrocarburos.",
	})
	if err := s.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")
	res, _ := s.BuscarLocal(ConsultaLocal{Texto: "hidrocarburos", Desde: desde, Hasta: hasta})
	if res.Total != 1 {
		t.Errorf("reindexar el día borró el texto ya bajado: total = %d", res.Total)
	}
}

// Indexar el texto de un aviso que no está en el índice no puede romper nada.
func TestIndexarDetalleDeAvisoDesconocido(t *testing.T) {
	s := nuevaSQLite(t)
	f, _ := boletin.ParseFecha("2026-09-01")
	err := s.IndexarDetalle(&boletin.Detalle{
		Aviso: boletin.Aviso{ID: "999", Seccion: boletin.Primera, Fecha: f},
		Texto: "algo",
	})
	if err != nil {
		t.Errorf("err = %v, se esperaba que lo ignore en silencio", err)
	}
}
