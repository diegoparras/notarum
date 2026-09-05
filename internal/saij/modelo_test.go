package saij

import "testing"

// Las materias se separan cuando la fuente las separa, y no se inventan
// cortes cuando no. Partir por espacios rompería "CULTURA Y EDUCACION".
func TestMaterias(t *testing.T) {
	conGuiones := Norma{TituloSumario: "EDUCACION SECUNDARIA-CENTRO DE ESTUDIANTES"}
	if got := conGuiones.Materias(); len(got) != 2 || got[0] != "EDUCACION SECUNDARIA" {
		t.Errorf("con guiones = %q", got)
	}
	// Este viene sin separadores en la base: se devuelve entero.
	pegadas := Norma{TituloSumario: "LENGUAJE DE SEÑAS CULTURA Y EDUCACION EDUCACION ESPECIAL"}
	if got := pegadas.Materias(); len(got) != 1 {
		t.Errorf("sin separadores se partió en %d: %q", len(got), got)
	}
	if got := (Norma{}).Materias(); got != nil {
		t.Errorf("sin sumario = %q", got)
	}
	// Los guiones sueltos no dejan elementos vacíos.
	sucio := Norma{TituloSumario: "UNA--OTRA-"}
	for _, m := range sucio.Materias() {
		if m == "" {
			t.Error("quedó una materia vacía")
		}
	}
}

func TestTituloSiempreDevuelveAlgo(t *testing.T) {
	// Muchas normas viejas no tienen ningún título cargado.
	pelada := Norma{Tipo: "Ley", Numero: "173", Provincia: "Río Negro"}
	if got := pelada.Titulo(); got != "Ley 173 de Río Negro" {
		t.Errorf("titulo = %q", got)
	}
	// El número 0 es el de las constituciones: no se escribe.
	consti := Norma{Tipo: "Constitución Provincial", Numero: "0", Provincia: "Salta"}
	if got := consti.Titulo(); got != "Constitución Provincial de Salta" {
		t.Errorf("titulo = %q", got)
	}
	// Y con título cargado, manda el título.
	conTitulo := Norma{Tipo: "Ley", Numero: "6109", TituloResumido: "Derecho a la identidad"}
	if got := conTitulo.Titulo(); got != "Derecho a la identidad" {
		t.Errorf("titulo = %q", got)
	}
}

func TestVigente(t *testing.T) {
	for estado, esperado := range map[string]bool{
		"Vigente, de alcance general": true,
		"Derogada":                    false,
		"Individual, Solo Modificatoria o Sin Eficacia": false,
		"No vigente, ley caduca":                        false,
		"Refundida, ley caduca":                         false,
		"Vetada":                                        false,
		"":                                              false,
	} {
		if got := (Norma{Estado: estado}).Vigente(); got != esperado {
			t.Errorf("%q -> %v", estado, got)
		}
	}
}

func TestBuscarProvincia(t *testing.T) {
	for entrada, esperado := range map[string]string{
		"Buenos Aires": "06", "buenos aires": "06", "06": "06", "LPB": "06",
		"Córdoba": "14", "cordoba": "14", "CABA": "02", "capital federal": "02",
		"Ciudad Autónoma de Buenos Aires": "02", "Tierra del Fuego": "94",
		"Río Negro": "62", "rio negro": "62", "santiago": "86",
	} {
		p, hay := BuscarProvincia(entrada)
		if !hay || p.ID != esperado {
			t.Errorf("%q -> %q (%v), se esperaba %q", entrada, p.ID, hay, esperado)
		}
	}
	for _, malo := range []string{"", "Montevideo", "99", "LPZZ", "asunción"} {
		if p, hay := BuscarProvincia(malo); hay {
			t.Errorf("%q se resolvió a %q", malo, p.Nombre)
		}
	}
}

func TestAnio(t *testing.T) {
	if got := (Norma{Fecha: "1994-09-13"}).Anio(); got != 1994 {
		t.Errorf("anio = %d", got)
	}
	for _, mala := range []string{"", "94", "sin fecha", "abcd-01-01"} {
		if got := (Norma{Fecha: mala}).Anio(); got != 0 {
			t.Errorf("%q -> %d", mala, got)
		}
	}
}

func TestURLFicha(t *testing.T) {
	n := Norma{ID: "LPB1000000"}
	if got := n.URLFicha(); got != "https://www.saij.gob.ar/LPB1000000" {
		t.Errorf("ficha = %q", got)
	}
	if got := (Norma{}).URLFicha(); got != "" {
		t.Errorf("sin id la ficha es %q", got)
	}
}
