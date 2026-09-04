package infoleg

import "testing"

// La ruta se calcula en carpetas de a cinco mil. Los casos con id conocido
// están verificados contra el sitio real: la Ley 27.742 (401266) devuelve su
// texto en la carpeta 400000-404999.
func TestURLTexto(t *testing.T) {
	casos := []struct {
		id       int
		esperado string
	}{
		{401266, BaseSitio + "/anexos/400000-404999/401266/norma.htm"},
		{374675, BaseSitio + "/anexos/370000-374999/374675/norma.htm"},
		{429014, BaseSitio + "/anexos/425000-429999/429014/norma.htm"},
		// Los bordes de una carpeta.
		{400000, BaseSitio + "/anexos/400000-404999/400000/norma.htm"},
		{404999, BaseSitio + "/anexos/400000-404999/404999/norma.htm"},
		{405000, BaseSitio + "/anexos/405000-409999/405000/norma.htm"},
		// Las primeras normas del catálogo.
		{1, BaseSitio + "/anexos/0-4999/1/norma.htm"},
	}
	for _, c := range casos {
		if got := URLTexto(c.id); got != c.esperado {
			t.Errorf("URLTexto(%d) = %q\n  se esperaba %q", c.id, got, c.esperado)
		}
	}
	if got := URLTexto(0); got != "" {
		t.Errorf("URLTexto(0) = %q, se esperaba vacío", got)
	}
	if got := URLTexto(-5); got != "" {
		t.Errorf("URLTexto(-5) = %q, se esperaba vacío", got)
	}
}

// Una norma sin texto publicado no puede ofrecer una URL de texto: sería un
// enlace roto. El catálogo dice cuáles antes de preguntarle a InfoLEG.
func TestNormaSinTextoNoDaURL(t *testing.T) {
	con := Norma{ID: 401266, TieneTexto: true}
	if con.URLTexto() == "" {
		t.Error("una norma con texto tiene que dar su URL")
	}
	sin := Norma{ID: 429014, TieneTexto: false}
	if sin.URLTexto() != "" {
		t.Errorf("una norma sin texto devolvió %q", sin.URLTexto())
	}
	// La ficha existe siempre, tenga texto o no.
	if sin.URLFicha() == "" {
		t.Error("la ficha tiene que existir igual")
	}
}

func TestParsearNorma(t *testing.T) {
	casos := []struct {
		texto  string
		tipo   string
		numero string
		anio   int
	}{
		{"Decreto 845/2026", "Decreto", "845", 2026},
		{"Ley 27742", "Ley", "27742", 0},
		{"Ley 27.742/2024", "", "", 0}, // con puntos no se reconoce el número
		{"Resolución 210/2026", "Resolución", "210", 2026},
		{"Resolucion 210/2026", "Resolución", "210", 2026}, // sin tilde
		{"RESOLUCIÓN 12/2026", "Resolución", "12", 2026},
		{"Resolución General 5678/2026", "Resolución General", "5678", 2026},
		{"Resolución Conjunta 3/2026", "Resolución Conjunta", "3", 2026},
		{"Decisión Administrativa 44/2026", "Decisión Administrativa", "44", 2026},
		{"Disposición 7/2026", "Disposición", "7", 2026},
		// Las sintetizadas son del mismo tipo en el catálogo.
		{"Resolución Sintetizada 434/2026", "Resolución", "434", 2026},
		{"Decreto 007/2026", "Decreto", "7", 2026}, // ceros a la izquierda
	}
	for _, c := range casos {
		t.Run(c.texto, func(t *testing.T) {
			ref, ok := ParsearNorma(c.texto)
			if c.tipo == "" {
				if ok {
					t.Errorf("se reconoció %q como %+v y no debería", c.texto, ref)
				}
				return
			}
			if !ok {
				t.Fatalf("no se reconoció %q", c.texto)
			}
			if ref.Tipo != c.tipo || ref.Numero != c.numero || ref.Anio != c.anio {
				t.Errorf("= %+v, se esperaba {%s %s %d}", ref, c.tipo, c.numero, c.anio)
			}
		})
	}
}

// La segunda y la tercera sección no nombran normas: no hay que inventar una.
func TestParsearNormaRechazaLoQueNoEsNorma(t *testing.T) {
	for _, texto := range []string{
		"", "   ", "PARTIDO FRENTE GRANDE", "Decreto", "845/2026",
		"AVISO OFICIAL", "Convocatoria a asamblea", "Sucesión de Pérez",
		"Licitación Pública 12/2026", // no es un tipo de norma del catálogo
	} {
		if ref, ok := ParsearNorma(texto); ok {
			t.Errorf("%q se reconoció como %+v", texto, ref)
		}
	}
}

func TestReferenciaSeEscribeComoLaNombraElBoletin(t *testing.T) {
	if got := (Referencia{Tipo: "Decreto", Numero: "845", Anio: 2026}).String(); got != "Decreto 845/2026" {
		t.Errorf("= %q", got)
	}
	if got := (Referencia{Tipo: "Ley", Numero: "27742"}).String(); got != "Ley 27742" {
		t.Errorf("= %q", got)
	}
}

func TestAnio(t *testing.T) {
	if a := (Norma{FechaBoletin: "2026-08-20"}).Anio(); a != 2026 {
		t.Errorf("= %d", a)
	}
	// Sin fecha de boletín se cae a la de sanción.
	if a := (Norma{FechaSancion: "1998-03-04"}).Anio(); a != 1998 {
		t.Errorf("= %d", a)
	}
	if a := (Norma{}).Anio(); a != 0 {
		t.Errorf("= %d", a)
	}
}

// La clave del catálogo y la que sale de un aviso tienen que coincidir: si no,
// el cruce no encuentra nada.
func TestClaveCoincideEntreCatalogoYAviso(t *testing.T) {
	// Como lo guarda el catálogo de InfoLEG.
	n := Norma{Tipo: "Decreto", Numero: "845", FechaBoletin: "2026-09-01"}
	// Como lo nombra el Boletín en el aviso.
	ref, ok := ParsearNorma("Decreto 845/2026")
	if !ok {
		t.Fatal("no se reconoció la norma del aviso")
	}
	if n.ClaveDe() != ref.Clave() {
		t.Errorf("catálogo = %q, aviso = %q: no se van a cruzar", n.ClaveDe(), ref.Clave())
	}
	if ref.Clave() != "normas/decreto/845/2026" {
		t.Errorf("clave = %q", ref.Clave())
	}
}

func TestClaveNormalizaElTipo(t *testing.T) {
	conAcento := Norma{Tipo: "Resolución", Numero: "210", FechaBoletin: "2026-08-20"}
	ref, _ := ParsearNorma("Resolucion 210/2026")
	if conAcento.ClaveDe() != ref.Clave() {
		t.Errorf("con acento = %q, sin acento = %q", conAcento.ClaveDe(), ref.Clave())
	}
	// Los tipos de dos palabras no pueden partir la clave en dos niveles.
	r2 := Referencia{Tipo: "Decisión Administrativa", Numero: "44", Anio: 2026}
	if r2.Clave() != "normas/decision-administrativa/44/2026" {
		t.Errorf("clave = %q", r2.Clave())
	}
}

func TestClaveVaciaSiNoHayNorma(t *testing.T) {
	if (Referencia{}).Clave() != "" {
		t.Error("una referencia vacía no puede dar clave")
	}
}
