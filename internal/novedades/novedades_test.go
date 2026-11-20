package novedades

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// muestra es un CHANGELOG chico con lo que el parser tiene que entender: el
// encabezado libre de arriba, versiones con fecha, secciones, un ítem que
// sigue en la línea de abajo con sangría y marcas de markdown.
const muestra = "# Changelog\n" +
	"\n" +
	"Formato basado en Keep a Changelog.\n" +
	"\n" +
	"## [0.3.0] - 2026-11-21\n" +
	"\n" +
	"### Nuevo\n" +
	"- **Novedades** en el lector: se ven al entrar\n" +
	"  despues de una actualizacion (`GET /api/novedades`).\n" +
	"\n" +
	"### Corregido\n" +
	"- Un arreglo corto.\n" +
	"\n" +
	"## [0.2.1] - 2026-11-20\n" +
	"\n" +
	"### Corregido\n" +
	"- Otro arreglo.\n" +
	"\n" +
	"## [0.2.0] - 2026-11-19\n" +
	"\n" +
	"### Nuevo\n" +
	"- Algo grande.\n"

func versiones(vs []Version) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.Version)
	}
	return out
}

func TestParsear(t *testing.T) {
	vs := Parsear(muestra)
	if got, want := versiones(vs), []string{"0.3.0", "0.2.1", "0.2.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("versiones = %v, se esperaba %v", got, want)
	}
	if vs[0].Fecha != "2026-11-21" {
		t.Errorf("fecha = %q", vs[0].Fecha)
	}
	if len(vs[0].Secciones) != 2 || vs[0].Secciones[0].Titulo != "Nuevo" || vs[0].Secciones[1].Titulo != "Corregido" {
		t.Fatalf("secciones = %+v", vs[0].Secciones)
	}
	want := "**Novedades** en el lector: se ven al entrar despues de una actualizacion (`GET /api/novedades`)."
	if got := vs[0].Secciones[0].Items; len(got) != 1 || got[0] != want {
		t.Errorf("item multilinea = %q, se esperaba %q", got, want)
	}
}

func TestParsearToleraCRLFyVacio(t *testing.T) {
	if vs := Parsear(""); len(vs) != 0 {
		t.Errorf("CHANGELOG vacío: %v", vs)
	}
	vs := Parsear(strings.ReplaceAll(muestra, "\n", "\r\n"))
	if len(vs) != 3 || vs[0].Secciones[1].Items[0] != "Un arreglo corto." {
		t.Errorf("con CRLF: %+v", vs)
	}
}

func TestDesde(t *testing.T) {
	vs := Parsear(muestra)
	casos := []struct {
		nombre, vista, actual string
		want                  []string
	}{
		{"de la mas nueva a la mas vieja", "0.2.0", "0.3.0", []string{"0.3.0", "0.2.1"}},
		{"ya vista: nada que mostrar", "0.3.0", "0.3.0", []string{}},
		{"primera vez: solo la actual", "", "0.3.0", []string{"0.3.0"}},
		{"nunca muestra versiones posteriores a la que corre", "0.2.0", "0.2.1", []string{"0.2.1"}},
		{"compara como version, no como texto", "0.10.0", "0.3.0", []string{}},
	}
	for _, c := range casos {
		if got := versiones(Desde(vs, c.vista, c.actual)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Desde(%q, %q) = %v, se esperaba %v", c.nombre, c.vista, c.actual, got, c.want)
		}
	}
	if got := versiones(Hasta(vs, "0.2.1")); !reflect.DeepEqual(got, []string{"0.2.1", "0.2.0"}) {
		t.Errorf("Hasta(0.2.1) = %v", got)
	}
}

func TestLineasLogYNotasMarkdown(t *testing.T) {
	v := Parsear(muestra)[0]
	want := []string{
		"Nuevo: Novedades en el lector: se ven al entrar despues de una actualizacion (GET /api/novedades).",
		"Corregido: Un arreglo corto.",
	}
	if got := LineasLog(v); !reflect.DeepEqual(got, want) {
		t.Errorf("LineasLog = %q", got)
	}
	md := NotasMarkdown(v)
	if !regexp.MustCompile(`^### Nuevo\n- \*\*Novedades\*\* en el lector`).MatchString(md) {
		t.Errorf("NotasMarkdown no arranca con la seccion: %q", md)
	}
	if !strings.Contains(md, "### Corregido\n- Un arreglo corto.") {
		t.Errorf("NotasMarkdown sin la segunda seccion: %q", md)
	}
}

func TestCartel(t *testing.T) {
	vs := Parsear(muestra)
	l := Cartel("NOTARUM", "0.3.0", vs, 60)
	for _, x := range l {
		if len([]rune(x)) > 60 {
			t.Errorf("renglon de %d columnas (pasa el ancho): %q", len([]rune(x)), x)
		}
		if strings.Contains(x, "**") || strings.Contains(x, "`") {
			t.Errorf("renglon con markdown: %q", x)
		}
	}
	raya := regexp.MustCompile(`^=+$`)
	if !raya.MatchString(l[0]) || !raya.MatchString(l[len(l)-1]) {
		t.Errorf("el cartel no abre y cierra con raya: %q", l)
	}
	if l[1] != "NOTARUM v0.3.0  (2026-11-21)" {
		t.Errorf("titulo = %q", l[1])
	}
	if l[2] != "Que cambia en esta version:" {
		t.Errorf("bajada = %q", l[2])
	}
	if l[3] != "- Nuevo: Novedades en el lector: se ven al entrar despues de" {
		t.Errorf("primer renglon = %q", l[3])
	}
	if !strings.HasPrefix(l[4], "    una actualizacion") {
		t.Errorf("la continuacion no queda sangrada: %q", l[4])
	}

	sin := Cartel("NOTARUM", "9.9.9", vs, 60)
	if sin[1] != "NOTARUM v9.9.9" || sin[2] != "Sin notas para esta version en el CHANGELOG." {
		t.Errorf("sin seccion: %q", sin)
	}
	if nada := Cartel("NOTARUM", "1.0.0", nil, 80); nada[2] != "Sin notas para esta version en el CHANGELOG." {
		t.Errorf("sin CHANGELOG: %q", nada)
	}
}

func TestBuscar(t *testing.T) {
	vs := Parsear(muestra)
	if v, ok := Buscar(vs, "0.2.1"); !ok || v.Version != "0.2.1" {
		t.Errorf("Buscar(0.2.1) = %+v, %v", v, ok)
	}
	if _, ok := Buscar(vs, "5.0.0"); ok {
		t.Error("Buscar encontró una versión que no está")
	}
}
