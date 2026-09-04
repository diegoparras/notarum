package contrato

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONEsValido(t *testing.T) {
	var v any
	if err := json.Unmarshal(JSON(), &v); err != nil {
		t.Fatalf("el contrato embebido no es JSON válido: %v", err)
	}
}

func TestLeer(t *testing.T) {
	d, err := Leer()
	if err != nil {
		t.Fatal(err)
	}
	if d.Titulo != "notarum" {
		t.Errorf("titulo = %q", d.Titulo)
	}
	if d.Version == "" || d.Descripcion == "" {
		t.Error("falta la versión o la descripción")
	}
	if len(d.Grupos) < 3 {
		t.Fatalf("grupos = %d", len(d.Grupos))
	}
	var rutas int
	for _, g := range d.Grupos {
		if g.Nombre == "" {
			t.Error("un grupo sin nombre")
		}
		rutas += len(g.Rutas)
	}
	if rutas < 9 {
		t.Errorf("rutas = %d: se esperaban todas las de la API", rutas)
	}
	if len(d.Esquemas) < 8 {
		t.Errorf("esquemas = %d", len(d.Esquemas))
	}
}

// Ninguna ruta puede quedar sin agrupar ni sin resumen: es lo que se lee.
func TestTodasLasRutasSonPresentables(t *testing.T) {
	d, err := Leer()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range d.Grupos {
		if g.Nombre == "otras" {
			t.Errorf("estas rutas no tienen tag en el contrato: %+v", g.Rutas)
		}
		for _, r := range g.Rutas {
			if r.Resumen == "" {
				t.Errorf("%s %s no tiene resumen", r.Metodo, r.Camino)
			}
			if r.Metodo == "" || r.Camino == "" {
				t.Errorf("ruta incompleta: %+v", r)
			}
			if len(r.Respuestas) == 0 {
				t.Errorf("%s %s no documenta ninguna respuesta", r.Metodo, r.Camino)
			}
		}
	}
}

// Los parámetros que el contrato declara por referencia tienen que quedar
// resueltos: si no, la página los muestra vacíos.
func TestLosParametrosPorReferenciaSeResuelven(t *testing.T) {
	d, err := Leer()
	if err != nil {
		t.Fatal(err)
	}
	var vistoSeccion bool
	for _, g := range d.Grupos {
		for _, r := range g.Rutas {
			for _, p := range r.Parametros {
				if p.Nombre == "" {
					t.Errorf("%s %s tiene un parámetro sin nombre: quedó sin resolver", r.Metodo, r.Camino)
				}
				if p.En == "" {
					t.Errorf("%s %s: el parámetro %q no dice dónde va", r.Metodo, r.Camino, p.Nombre)
				}
				if p.Nombre == "seccion" && len(p.Opciones) > 0 {
					vistoSeccion = true
				}
			}
		}
	}
	if !vistoSeccion {
		t.Error("el parámetro seccion tendría que traer sus opciones (primera, segunda, tercera)")
	}
}

// El ejemplo se arma reemplazando los huecos: si queda alguno, no sirve para
// copiar y pegar.
func TestLosEjemplosNoTienenHuecos(t *testing.T) {
	d, err := Leer()
	if err != nil {
		t.Fatal(err)
	}
	var conEjemplo int
	for _, g := range d.Grupos {
		for _, r := range g.Rutas {
			if r.Ejemplo == "" {
				continue
			}
			conEjemplo++
			if strings.Contains(r.Ejemplo, "{") || strings.Contains(r.Ejemplo, "}") {
				t.Errorf("%s tiene un hueco sin llenar: %q", r.Camino, r.Ejemplo)
			}
		}
	}
	if conEjemplo < 5 {
		t.Errorf("sólo %d rutas tienen ejemplo: la mayoría debería tenerlo", conEjemplo)
	}
}

func TestEjemploConcreto(t *testing.T) {
	d, err := Leer()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range d.Grupos {
		for _, r := range g.Rutas {
			if r.Camino == "/v1/ediciones/{seccion}/{fecha}" {
				if r.Ejemplo != "/v1/ediciones/primera/2026-09-01" {
					t.Errorf("ejemplo = %q", r.Ejemplo)
				}
				return
			}
		}
	}
	t.Error("no se encontró la ruta de la edición")
}

// Un esquema compuesto con allOf tiene que mostrar también los campos que
// hereda: si no, el detalle del aviso parece tener sólo tres campos.
func TestEsquemaCompuestoHeredaCampos(t *testing.T) {
	d, err := Leer()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range d.Esquemas {
		if e.Nombre != "Detalle" {
			continue
		}
		campos := map[string]Campo{}
		for _, c := range e.Campos {
			campos[c.Nombre] = c
		}
		// Propios del detalle.
		for _, n := range []string{"texto", "html", "anexos"} {
			if _, hay := campos[n]; !hay {
				t.Errorf("falta el campo propio %q", n)
			}
		}
		// Heredados del aviso.
		for _, n := range []string{"id", "seccion", "organismo", "tiene_anexos"} {
			if _, hay := campos[n]; !hay {
				t.Errorf("falta el campo heredado %q", n)
			}
		}
		if c := campos["anexos"]; c.Tipo != "lista de Anexo" {
			t.Errorf("tipo de anexos = %q", c.Tipo)
		}
		return
	}
	t.Error("no se encontró el esquema Detalle")
}

func TestTiposLegibles(t *testing.T) {
	d, err := Leer()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range d.Esquemas {
		for _, c := range e.Campos {
			if c.Tipo == "" {
				t.Errorf("%s.%s no tiene tipo", e.Nombre, c.Nombre)
			}
			if strings.Contains(c.Tipo, "#/") {
				t.Errorf("%s.%s muestra una referencia cruda: %q", e.Nombre, c.Nombre, c.Tipo)
			}
		}
	}
}
