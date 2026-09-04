package almacen

import (
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
)

// Este archivo tiene la suite que TODO motor de almacenamiento tiene que
// pasar. Es lo que hace que elegir disco, sqlite o postgres sea una decisión
// de operación y no de comportamiento: si un motor se desvía, acá se rompe.
//
// Cada motor la invoca desde su propio archivo de tests.

// probarAlmacen corre el contrato básico de guardado contra un motor.
func probarAlmacen(t *testing.T, nuevo func(t *testing.T) Almacen) {
	t.Helper()

	t.Run("guardar y leer", func(t *testing.T) {
		a := nuevo(t)
		if _, ok := a.Leer("no-esta"); ok {
			t.Error("un almacén vacío devolvió un acierto")
		}
		if err := a.Guardar("ediciones/primera/2026-09-01", []byte(`{"a":1}`), SinVencimiento); err != nil {
			t.Fatal(err)
		}
		datos, ok := a.Leer("ediciones/primera/2026-09-01")
		if !ok {
			t.Fatal("no se leyó lo que se acababa de guardar")
		}
		if string(datos) != `{"a":1}` {
			t.Errorf("datos = %s", datos)
		}
	})

	t.Run("guardar pisa lo anterior", func(t *testing.T) {
		a := nuevo(t)
		_ = a.Guardar("x", []byte(`"viejo"`), SinVencimiento)
		_ = a.Guardar("x", []byte(`"nuevo"`), SinVencimiento)
		datos, _ := a.Leer("x")
		if string(datos) != `"nuevo"` {
			t.Errorf("datos = %s", datos)
		}
	})

	t.Run("vencimiento", func(t *testing.T) {
		a := nuevo(t)
		if err := a.Guardar("hoy", []byte(`1`), 150*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if _, ok := a.Leer("hoy"); !ok {
			t.Fatal("debería estar vigente recién guardada")
		}
		time.Sleep(250 * time.Millisecond)
		if _, ok := a.Leer("hoy"); ok {
			t.Error("se devolvió una entrada vencida")
		}
		if a.Existe("hoy") {
			t.Error("Existe devolvió true para una entrada vencida")
		}
	})

	t.Run("sin vencimiento no caduca", func(t *testing.T) {
		a := nuevo(t)
		if err := a.Guardar("pasada", []byte(`1`), SinVencimiento); err != nil {
			t.Fatal(err)
		}
		time.Sleep(250 * time.Millisecond)
		if _, ok := a.Leer("pasada"); !ok {
			t.Error("una edición pasada no debería caducar")
		}
	})

	t.Run("borrar", func(t *testing.T) {
		a := nuevo(t)
		_ = a.Guardar("x", []byte(`1`), SinVencimiento)
		if err := a.Borrar("x"); err != nil {
			t.Fatal(err)
		}
		if a.Existe("x") {
			t.Error("la entrada sigue después de borrarla")
		}
		if err := a.Borrar("x"); err != nil {
			t.Errorf("borrar dos veces debería ser inocuo: %v", err)
		}
	})

	t.Run("claves invalidas", func(t *testing.T) {
		a := nuevo(t)
		if err := a.Guardar("", []byte(`1`), SinVencimiento); err == nil {
			t.Error("se aceptó una clave vacía")
		}
		if _, ok := a.Leer(""); ok {
			t.Error("una clave vacía devolvió datos")
		}
	})

	t.Run("metricas", func(t *testing.T) {
		a := nuevo(t)
		a.Leer("no-esta")
		_ = a.Guardar("esta", []byte(`1`), SinVencimiento)
		a.Leer("esta")
		m := a.Metricas()
		if m.Motor == "" {
			t.Error("el motor no se identifica")
		}
		if m.Aciertos != 1 || m.Fallos != 1 || m.Escritos != 1 {
			t.Errorf("metricas = %+v", m)
		}
		if m.Entradas != 1 {
			t.Errorf("entradas = %d, se esperaba 1", m.Entradas)
		}
	})

	t.Run("datos binarios y unicode", func(t *testing.T) {
		a := nuevo(t)
		// Los avisos vienen llenos de acentos y de comillas; el almacén no
		// puede alterarlos.
		valor := []byte(`{"organismo":"SECRETARÍA DE ENERGÍA","nota":"«ñandú» \"x\""}`)
		if err := a.Guardar("raro", valor, SinVencimiento); err != nil {
			t.Fatal(err)
		}
		vuelto, ok := a.Leer("raro")
		if !ok || string(vuelto) != string(valor) {
			t.Errorf("volvió %q", vuelto)
		}
	})
}

// probarIndexador corre el contrato de búsqueda contra un motor que indexa.
func probarIndexador(t *testing.T, nuevo func(t *testing.T) Indexador) {
	t.Helper()

	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")

	t.Run("indexar y buscar", func(t *testing.T) {
		ix := nuevo(t)
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		casos := []struct {
			nombre   string
			q        ConsultaLocal
			esperado int
		}{
			{"por organismo", ConsultaLocal{Texto: "aduana"}, 1},
			{"por norma", ConsultaLocal{Texto: "decreto"}, 2},
			{"por sintesis", ConsultaLocal{Texto: "designaciones"}, 1},
			{"dos palabras", ConsultaLocal{Texto: "ministerio salud"}, 1},
			{"sin resultados", ConsultaLocal{Texto: "petroleo"}, 0},
			{"filtrado por rubro", ConsultaLocal{Rubro: "DECRETOS"}, 2},
			{"texto y rubro", ConsultaLocal{Texto: "decreto", Rubro: "RESOLUCIONES"}, 0},
			{"todo el rango", ConsultaLocal{}, 3},
		}
		for _, c := range casos {
			t.Run(c.nombre, func(t *testing.T) {
				c.q.Desde, c.q.Hasta = desde, hasta
				res, err := ix.BuscarLocal(c.q)
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
	})

	// Nadie escribe los acentos en una caja de búsqueda.
	t.Run("ignora acentos", func(t *testing.T) {
		ix := nuevo(t)
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		for _, texto := range []string{"promúlgase", "promulgase", "PROMULGASE"} {
			res, err := ix.BuscarLocal(ConsultaLocal{Texto: texto, Desde: desde, Hasta: hasta})
			if err != nil {
				t.Fatal(err)
			}
			if res.Total != 1 {
				t.Errorf("buscando %q: total = %d, se esperaba 1", texto, res.Total)
			}
		}
	})

	// La búsqueda no puede romperse ni inyectar por lo que escriba alguien.
	t.Run("tolera texto raro", func(t *testing.T) {
		ix := nuevo(t)
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		raros := []string{`"`, `foo"bar`, `*`, `AND`, `OR NOT`, `a" OR "b`,
			`'; DROP TABLE avisos;--`, `()`, `^`, `NEAR(`, `\`, `%`, `_`}
		for _, texto := range raros {
			if _, err := ix.BuscarLocal(ConsultaLocal{Texto: texto, Desde: desde, Hasta: hasta}); err != nil {
				t.Errorf("buscando %q devolvió error: %v", texto, err)
			}
		}
		res, err := ix.BuscarLocal(ConsultaLocal{Desde: desde, Hasta: hasta})
		if err != nil || res.Total != 3 {
			t.Errorf("después de los textos raros: total = %d, err = %v", res.Total, err)
		}
	})

	t.Run("filtra por seccion y fecha", func(t *testing.T) {
		ix := nuevo(t)
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		res, _ := ix.BuscarLocal(ConsultaLocal{Seccion: boletin.Segunda, Desde: desde, Hasta: hasta})
		if res.Total != 0 {
			t.Errorf("la segunda no tiene nada indexado y devolvió %d", res.Total)
		}
		f1, _ := boletin.ParseFecha("2026-10-01")
		f2, _ := boletin.ParseFecha("2026-10-31")
		res, _ = ix.BuscarLocal(ConsultaLocal{Desde: f1, Hasta: f2})
		if res.Total != 0 {
			t.Errorf("fuera del rango devolvió %d", res.Total)
		}
	})

	t.Run("pagina", func(t *testing.T) {
		ix := nuevo(t)
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		p1, err := ix.BuscarLocal(ConsultaLocal{Desde: desde, Hasta: hasta, Limite: 2})
		if err != nil {
			t.Fatal(err)
		}
		if p1.Total != 3 || len(p1.Avisos) != 2 {
			t.Fatalf("página 1: total = %d, avisos = %d", p1.Total, len(p1.Avisos))
		}
		p2, err := ix.BuscarLocal(ConsultaLocal{Desde: desde, Hasta: hasta, Limite: 2, Desplazamiento: 2})
		if err != nil {
			t.Fatal(err)
		}
		if p2.Total != 3 || len(p2.Avisos) != 1 {
			t.Fatalf("página 2: total = %d, avisos = %d", p2.Total, len(p2.Avisos))
		}
		if p1.Avisos[0].ID == p2.Avisos[0].ID {
			t.Error("las dos páginas devolvieron el mismo aviso")
		}
	})

	t.Run("reindexar no duplica", func(t *testing.T) {
		ix := nuevo(t)
		ed := edicionDePrueba(t)
		for i := 0; i < 3; i++ {
			if err := ix.IndexarEdicion(ed); err != nil {
				t.Fatal(err)
			}
		}
		res, _ := ix.BuscarLocal(ConsultaLocal{Desde: desde, Hasta: hasta})
		if res.Total != 3 {
			t.Errorf("total = %d, se esperaban 3: se duplicaron avisos", res.Total)
		}
	})

	t.Run("devuelve el aviso entero", func(t *testing.T) {
		ix := nuevo(t)
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		res, err := ix.BuscarLocal(ConsultaLocal{Texto: "aduana", Desde: desde, Hasta: hasta})
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
	})

	// El texto completo del aviso llega al índice cuando ya se bajó, y
	// reindexar el día no puede borrar lo que costó traer.
	t.Run("texto completo del aviso", func(t *testing.T) {
		ix := nuevo(t)
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		if res, _ := ix.BuscarLocal(ConsultaLocal{Texto: "hidrocarburos", Desde: desde, Hasta: hasta}); res.Total != 0 {
			t.Fatalf("antes de indexar el cuerpo ya encontraba %d", res.Total)
		}
		f, _ := boletin.ParseFecha("2026-09-01")
		d := &boletin.Detalle{
			Aviso: boletin.Aviso{ID: "1", Seccion: boletin.Primera, Fecha: f},
			Texto: "VISTO el régimen de hidrocarburos y la promoción de inversiones.",
		}
		if err := ix.IndexarDetalle(d); err != nil {
			t.Fatal(err)
		}
		res, err := ix.BuscarLocal(ConsultaLocal{Texto: "hidrocarburos", Desde: desde, Hasta: hasta})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 1 {
			t.Fatalf("total = %d, se esperaba 1", res.Total)
		}
		if res.Avisos[0].Organismo != "PODER EJECUTIVO" {
			t.Errorf("indexar el cuerpo pisó el resto del aviso: %+v", res.Avisos[0])
		}
		// Reindexar el día conserva el texto ya bajado.
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		if r2, _ := ix.BuscarLocal(ConsultaLocal{Texto: "hidrocarburos", Desde: desde, Hasta: hasta}); r2.Total != 1 {
			t.Errorf("reindexar borró el texto ya bajado: total = %d", r2.Total)
		}
		// Y lo del sumario se sigue encontrando.
		if r3, _ := ix.BuscarLocal(ConsultaLocal{Texto: "promúlgase", Desde: desde, Hasta: hasta}); r3.Total != 1 {
			t.Errorf("se perdió la búsqueda por sumario: %d", r3.Total)
		}
	})

	t.Run("texto de un aviso desconocido", func(t *testing.T) {
		ix := nuevo(t)
		f, _ := boletin.ParseFecha("2026-09-01")
		err := ix.IndexarDetalle(&boletin.Detalle{
			Aviso: boletin.Aviso{ID: "999", Seccion: boletin.Primera, Fecha: f},
			Texto: "algo",
		})
		if err != nil {
			t.Errorf("err = %v, se esperaba que lo ignore en silencio", err)
		}
	})

	t.Run("cobertura", func(t *testing.T) {
		ix := nuevo(t)
		d1, _ := boletin.ParseFecha("2026-09-01")
		d2, _ := boletin.ParseFecha("2026-09-30")
		n, err := ix.Cobertura(boletin.Primera, d1, d2)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("sin indexar, cobertura = %d", n)
		}
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		n, err = ix.Cobertura(boletin.Primera, d1, d2)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("cobertura = %d, se esperaba 1 día indexado", n)
		}
	})

	t.Run("metricas del indice", func(t *testing.T) {
		ix := nuevo(t)
		if err := ix.IndexarEdicion(edicionDePrueba(t)); err != nil {
			t.Fatal(err)
		}
		if m := ix.Metricas(); m.Avisos != 3 {
			t.Errorf("avisos indexados = %d, se esperaban 3", m.Avisos)
		}
	})
}

// Los tres motores cumplen la misma interfaz.
func TestTodosCumplenLaInterfaz(t *testing.T) {
	var _ Almacen = (*Disco)(nil)
	var _ Almacen = (*SQLite)(nil)
	var _ Almacen = (*Postgres)(nil)
	var _ Indexador = (*SQLite)(nil)
	var _ Indexador = (*Postgres)(nil)
}

func TestConformidadDisco(t *testing.T) {
	probarAlmacen(t, func(t *testing.T) Almacen {
		d, err := NuevoDisco(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return d
	})
}

func TestConformidadSQLite(t *testing.T) {
	probarAlmacen(t, func(t *testing.T) Almacen { return nuevaSQLite(t) })
	probarIndexador(t, func(t *testing.T) Indexador { return nuevaSQLite(t) })
}
