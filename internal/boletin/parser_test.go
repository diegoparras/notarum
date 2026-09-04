package boletin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, nombre string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", nombre))
	if err != nil {
		t.Fatalf("no se pudo leer el fixture %s: %v", nombre, err)
	}
	return b
}

// Las cantidades son las que devolvió el sitio el 4/9/2026; si el parser
// cambia y deja de coincidir, es que rompimos la extracción.
func TestParsearPortadaCantidades(t *testing.T) {
	casos := []struct {
		fixture  string
		seccion  Seccion
		fecha    string
		cantidad int
	}{
		{"portada_primera_20260901.html", Primera, "2026-09-01", 100},
		{"portada_primera_20260715.html", Primera, "2026-07-15", 73},
		{"portada_segunda_20260901.html", Segunda, "2026-09-01", 100},
		{"portada_tercera_20260901.html", Tercera, "2026-09-01", 54},
		{"portada_tercera_rubro1566_20260901.html", Tercera, "2026-09-01", 9},
		{"portada_primera_suplemento_20260814.html", Primera, "2026-08-14", 21},
	}
	for _, c := range casos {
		t.Run(c.fixture, func(t *testing.T) {
			fecha, _ := ParseFecha(c.fecha)
			ed, err := ParsearPortada(fixture(t, c.fixture), c.seccion, fecha)
			if err != nil {
				t.Fatalf("ParsearPortada: %v", err)
			}
			if ed.Cantidad != c.cantidad {
				t.Errorf("cantidad = %d, se esperaba %d", ed.Cantidad, c.cantidad)
			}
			if len(ed.Avisos) != c.cantidad {
				t.Errorf("len(avisos) = %d, se esperaba %d", len(ed.Avisos), c.cantidad)
			}
			var suma int
			for _, n := range ed.PorRubro {
				suma += n
			}
			if suma != c.cantidad {
				t.Errorf("por_rubro suma %d, se esperaba %d", suma, c.cantidad)
			}
		})
	}
}

func TestParsearPortadaCamposDelPrimerAviso(t *testing.T) {
	fecha, _ := ParseFecha("2026-09-01")
	ed, err := ParsearPortada(fixture(t, "portada_primera_20260901.html"), Primera, fecha)
	if err != nil {
		t.Fatal(err)
	}
	a := ed.Avisos[0]
	if a.ID != "346633" {
		t.Errorf("id = %q, se esperaba 346633", a.ID)
	}
	if a.Rubro != "DECRETOS" {
		t.Errorf("rubro = %q, se esperaba DECRETOS", a.Rubro)
	}
	if a.Organismo != "PODER EJECUTIVO" {
		t.Errorf("organismo = %q, se esperaba PODER EJECUTIVO", a.Organismo)
	}
	if a.Norma != "Decreto 845/2026" {
		t.Errorf("norma = %q, se esperaba Decreto 845/2026", a.Norma)
	}
	if a.Referencia != "DECTO-2026-845-APN-PTE" {
		t.Errorf("referencia = %q", a.Referencia)
	}
	if a.Sintesis != "Disposiciones." {
		t.Errorf("sintesis = %q", a.Sintesis)
	}
	if !a.TieneAnexos {
		t.Error("tiene_anexos = false, el aviso 346633 tiene el clip")
	}
	if a.Fecha.API() != "2026-09-01" {
		t.Errorf("fecha = %s", a.Fecha.API())
	}
	const url = "https://www.boletinoficial.gob.ar/detalleAviso/primera/346633/20260901"
	if a.URL != url {
		t.Errorf("url = %q", a.URL)
	}
}

// Los avisos de la segunda traen sólo el organismo y su id es alfanumérico.
func TestParsearPortadaSegundaIDAlfanumerico(t *testing.T) {
	fecha, _ := ParseFecha("2026-09-01")
	ed, err := ParsearPortada(fixture(t, "portada_segunda_20260901.html"), Segunda, fecha)
	if err != nil {
		t.Fatal(err)
	}
	a := ed.Avisos[0]
	if a.ID != "A1522579" {
		t.Errorf("id = %q, se esperaba A1522579", a.ID)
	}
	if a.Organismo != "PARTIDO FRENTE GRANDE" {
		t.Errorf("organismo = %q", a.Organismo)
	}
	if a.Norma != "" || a.Sintesis != "" {
		t.Errorf("la segunda no trae norma ni sintesis: norma=%q sintesis=%q", a.Norma, a.Sintesis)
	}
}

// Un rubro terminado en "- ANTERIOR" agrupa avisos ya publicados antes.
func TestParsearPortadaMarcaRepetidos(t *testing.T) {
	fecha, _ := ParseFecha("2026-07-15")
	ed, err := ParsearPortada(fixture(t, "portada_primera_20260715.html"), Primera, fecha)
	if err != nil {
		t.Fatal(err)
	}
	var repetidos, normales int
	for _, a := range ed.Avisos {
		if a.Repetido {
			repetidos++
			if !strings.Contains(strings.ToUpper(a.Rubro), "ANTERIOR") {
				t.Errorf("aviso %s marcado repetido pero su rubro es %q", a.ID, a.Rubro)
			}
		} else {
			normales++
			if strings.Contains(strings.ToUpper(a.Rubro), "ANTERIOR") {
				t.Errorf("aviso %s en rubro ANTERIOR pero no esta marcado repetido", a.ID)
			}
		}
	}
	if repetidos == 0 {
		t.Error("la edicion del 15/7/2026 tiene rubros ANTERIOR y no se marco ninguno")
	}
	if normales == 0 {
		t.Error("no quedo ningun aviso sin marcar")
	}
}

// Una página que mezcla fechas es un error, no una edición.
func TestParsearPortadaRechazaFechaDistinta(t *testing.T) {
	otra, _ := ParseFecha("2020-01-02")
	if _, err := ParsearPortada(fixture(t, "portada_primera_20260901.html"), Primera, otra); err == nil {
		t.Fatal("se esperaba error al pedir una fecha que no es la de la pagina")
	}
}

func TestParsearDetalle(t *testing.T) {
	fecha, _ := ParseFecha("2026-09-01")
	d, err := ParsearDetalle(fixture(t, "detalle_primera_346633.html"), Primera, "346633", fecha)
	if err != nil {
		t.Fatal(err)
	}
	if d.Organismo != "PODER EJECUTIVO" {
		t.Errorf("organismo = %q", d.Organismo)
	}
	if d.Norma != "Decreto 845/2026" {
		t.Errorf("norma = %q", d.Norma)
	}
	if d.Referencia != "DECTO-2026-845-APN-PTE" {
		t.Errorf("referencia = %q", d.Referencia)
	}
	if d.FechaPublicacion != "2026-09-01" {
		t.Errorf("fecha_publicacion = %q, se esperaba 2026-09-01", d.FechaPublicacion)
	}
	if len(d.Texto) < 200 {
		t.Errorf("el texto plano quedo en %d caracteres, se esperaba el cuerpo completo", len(d.Texto))
	}
	if strings.Contains(d.HTML, "<script") || strings.Contains(d.HTML, "<style") {
		t.Error("el HTML limpio todavia trae script o style")
	}
	if strings.Contains(d.HTML, "<html") || strings.Contains(d.HTML, "<body") {
		t.Error("el HTML limpio todavia trae los html/body anidados del origen")
	}
	// Los 12 anexos de este aviso comparten el mismo idAnexo y se distinguen
	// por nroAnexo: si se deduplica por id solo, quedan reducidos a uno.
	if len(d.Anexos) != 12 {
		t.Fatalf("anexos = %d, se esperaban 12", len(d.Anexos))
	}
	numeros := map[string]bool{}
	for _, a := range d.Anexos {
		if a.ID == "" || a.URL == "" || a.Numero == "" {
			t.Errorf("anexo incompleto: %+v", a)
		}
		if numeros[a.Numero] {
			t.Errorf("número de anexo repetido: %s", a.Numero)
		}
		numeros[a.Numero] = true
	}
	if d.Anexos[0].URL != "/v1/anexos/primera/1/7756488/2026-09-01.pdf" {
		t.Errorf("url del anexo = %q", d.Anexos[0].URL)
	}
	if !d.TieneAnexos {
		t.Error("tiene_anexos = false pese a haber anexos")
	}
}

func TestParsearDetalleSegunda(t *testing.T) {
	fecha, _ := ParseFecha("2026-09-01")
	d, err := ParsearDetalle(fixture(t, "detalle_segunda_A1522579.html"), Segunda, "A1522579", fecha)
	if err != nil {
		t.Fatal(err)
	}
	if d.Organismo == "" {
		t.Error("organismo vacio")
	}
	if d.Texto == "" {
		t.Error("texto vacio")
	}
}

// El calendario viene como un string JSON adentro de otro JSON.
func TestParsearCalendario(t *testing.T) {
	cal, err := ParsearCalendario(fixture(t, "calendario_primera_2026.json"), Primera, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Fechas) < 150 {
		t.Errorf("len(fechas) = %d, se esperaban los dias habiles del anio", len(cal.Fechas))
	}
	if cal.Fechas[0].API() != "2026-01-02" {
		t.Errorf("primera fecha = %s, se esperaba 2026-01-02", cal.Fechas[0].API())
	}
	// El 17/8/2026 es feriado: no debe estar.
	for _, f := range cal.Fechas {
		if f.API() == "2026-08-17" {
			t.Error("2026-08-17 es feriado y aparece en el calendario")
		}
	}
	if len(cal.ConSuplemento) == 0 {
		t.Error("la primera seccion tuvo suplementos en 2026 y la lista vino vacia")
	}
}

func TestParsearRubros(t *testing.T) {
	rs, err := ParsearRubros(fixture(t, "rubros_primera.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) < 5 {
		t.Fatalf("len(rubros) = %d", len(rs))
	}
	if rs[0].ID == "" || rs[0].Nombre == "" {
		t.Errorf("rubro incompleto: %+v", rs[0])
	}
	var vistoDecretos bool
	for _, r := range rs {
		if strings.EqualFold(r.Nombre, "DECRETOS") {
			vistoDecretos = true
		}
	}
	if !vistoDecretos {
		t.Error("no aparecio el rubro DECRETOS en la primera seccion")
	}
}

func TestParsearBusqueda(t *testing.T) {
	res, err := ParsearBusqueda(fixture(t, "busqueda_primera.json"), Primera, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cantidad == 0 {
		t.Fatal("la busqueda no devolvio avisos")
	}
	if res.Avisos[0].ID == "" {
		t.Errorf("aviso sin id: %+v", res.Avisos[0])
	}
	if res.Avisos[0].Seccion != Primera {
		t.Errorf("seccion = %q", res.Avisos[0].Seccion)
	}
}

// El Boletín publica algunos avisos sin organismo: en el rubro LEYES, los
// decretos de promulgación traen <p class="item"> </p> vacío. Es un dato
// legítimo, no una falla de extracción.
func TestParsearPortadaAceptaAvisoSinOrganismo(t *testing.T) {
	fecha, _ := ParseFecha("2025-03-10")
	ed, err := ParsearPortada(fixture(t, "portada_primera_20250310.html"), Primera, fecha)
	if err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 52 {
		t.Errorf("cantidad = %d, se esperaban 52", ed.Cantidad)
	}
	var sinOrganismo *Aviso
	for i := range ed.Avisos {
		if ed.Avisos[i].ID == "322274" {
			sinOrganismo = &ed.Avisos[i]
		}
	}
	if sinOrganismo == nil {
		t.Fatal("no se extrajo el aviso 322274")
	}
	if sinOrganismo.Organismo != "" {
		t.Errorf("organismo = %q, se esperaba vacio", sinOrganismo.Organismo)
	}
	if sinOrganismo.Rubro != "LEYES" || sinOrganismo.Norma != "Decreto 177/2025" {
		t.Errorf("rubro = %q, norma = %q", sinOrganismo.Rubro, sinOrganismo.Norma)
	}
}
