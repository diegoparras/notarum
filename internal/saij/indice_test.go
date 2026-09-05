package saij

import (
	"strings"
	"testing"
)

func indiceDePrueba(t *testing.T) *Indice {
	t.Helper()
	i := NuevoIndice()
	if _, err := i.Cargar(abrirFixture(t)); err != nil {
		t.Fatal(err)
	}
	return i
}

func TestIndiceCarga(t *testing.T) {
	i := indiceDePrueba(t)
	if i.Normas() != 48 {
		t.Fatalf("normas = %d", i.Normas())
	}
	if !i.Cargado() {
		t.Error("dice no estar cargado")
	}
	if n := NuevoIndice(); n.Cargado() {
		t.Error("un índice vacío dice estar cargado")
	}
}

func TestBuscarPorTexto(t *testing.T) {
	i := indiceDePrueba(t)
	// El fixture tiene 16 constituciones provinciales.
	r := i.Buscar(Consulta{Texto: "constitución"})
	if r.Total == 0 {
		t.Fatal("no se encontró ninguna constitución")
	}
	for _, n := range r.Normas {
		junto := normalizar(n.Titulo() + " " + n.Tipo + " " + n.TituloSumario)
		if !strings.Contains(junto, "constitucion") {
			t.Errorf("%s no tiene que ver con la búsqueda: %q", n.ID, n.Titulo())
		}
	}
}

// Buscar sin acentos tiene que encontrar lo acentuado, y al revés: nadie
// escribe "Constitución" con acento en un buscador.
func TestLosAcentosNoImportan(t *testing.T) {
	i := indiceDePrueba(t)
	con := i.Buscar(Consulta{Texto: "constitución", Limite: LimiteMaximo})
	sin := i.Buscar(Consulta{Texto: "constitucion", Limite: LimiteMaximo})
	if con.Total != sin.Total || con.Total == 0 {
		t.Fatalf("con acento %d, sin acento %d", con.Total, sin.Total)
	}
	mayus := i.Buscar(Consulta{Texto: "CONSTITUCION", Limite: LimiteMaximo})
	if mayus.Total != sin.Total {
		t.Errorf("en mayúsculas da %d", mayus.Total)
	}
}

// Todas las palabras tienen que estar: buscar dos cosas no puede devolver lo
// que sólo tiene una.
func TestLasPalabrasSeSuman(t *testing.T) {
	i := indiceDePrueba(t)
	una := i.Buscar(Consulta{Texto: "constitución", Limite: LimiteMaximo})
	dos := i.Buscar(Consulta{Texto: "constitución salta", Limite: LimiteMaximo})
	if dos.Total > una.Total {
		t.Errorf("sumar una palabra amplió el resultado: %d -> %d", una.Total, dos.Total)
	}
	for _, n := range dos.Normas {
		if !strings.Contains(normalizar(n.Provincia+n.Titulo()+n.TituloSumario), "salta") {
			t.Errorf("%s no menciona salta", n.ID)
		}
	}
}

func TestFiltrarPorProvincia(t *testing.T) {
	i := indiceDePrueba(t)
	// Por nombre, por código y por prefijo tiene que dar lo mismo.
	porNombre := i.Buscar(Consulta{Provincia: "Chaco", Limite: LimiteMaximo})
	porCodigo := i.Buscar(Consulta{Provincia: "22", Limite: LimiteMaximo})
	porPrefijo := i.Buscar(Consulta{Provincia: "LPH", Limite: LimiteMaximo})
	if porNombre.Total == 0 {
		t.Fatal("no hay normas de Chaco en el fixture")
	}
	if porNombre.Total != porCodigo.Total || porNombre.Total != porPrefijo.Total {
		t.Errorf("nombre %d, código %d, prefijo %d", porNombre.Total, porCodigo.Total, porPrefijo.Total)
	}
	for _, n := range porNombre.Normas {
		if n.ProvinciaID != "22" {
			t.Errorf("%s es de %s", n.ID, n.Provincia)
		}
	}
}

// Una provincia que no existe devuelve nada, no todo. Devolver todo sería
// contestar otra pregunta.
func TestProvinciaQueNoExiste(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{Provincia: "Montevideo"})
	if r.Total != 0 {
		t.Errorf("devolvió %d normas de una provincia que no existe", r.Total)
	}
}

func TestFiltrarPorAnio(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{Desde: 1990, Hasta: 2000, Limite: LimiteMaximo})
	for _, n := range r.Normas {
		if a := n.Anio(); a < 1990 || a > 2000 {
			t.Errorf("%s es de %d", n.ID, a)
		}
	}
	// Los extremos sueltos también sirven.
	viejas := i.Buscar(Consulta{Hasta: 1900, Limite: LimiteMaximo})
	for _, n := range viejas.Normas {
		if n.Anio() > 1900 {
			t.Errorf("%s es de %d y se pidió hasta 1900", n.ID, n.Anio())
		}
	}
}

func TestSoloVigentes(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{SoloVigentes: true, Limite: LimiteMaximo})
	if r.Total == 0 {
		t.Fatal("ninguna vigente en el fixture")
	}
	for _, n := range r.Normas {
		if !n.Vigente() {
			t.Errorf("%s está %q", n.ID, n.Estado)
		}
	}
	// Y sin el filtro tienen que aparecer más.
	todas := i.Buscar(Consulta{Limite: LimiteMaximo})
	if todas.Total <= r.Total {
		t.Errorf("con filtro %d, sin filtro %d", r.Total, todas.Total)
	}
}

// De la más nueva a la más vieja, que es como se busca normativa.
func TestElOrdenEsPorFecha(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{Limite: LimiteMaximo})
	for k := 1; k < len(r.Normas); k++ {
		if r.Normas[k-1].Fecha < r.Normas[k].Fecha {
			t.Fatalf("desordenadas: %s (%s) antes que %s (%s)",
				r.Normas[k-1].ID, r.Normas[k-1].Fecha, r.Normas[k].ID, r.Normas[k].Fecha)
		}
	}
}

// Dos consultas iguales devuelven lo mismo en el mismo orden: si no, paginar
// saltea o repite.
func TestElOrdenEsEstable(t *testing.T) {
	i := indiceDePrueba(t)
	una := i.Buscar(Consulta{Limite: LimiteMaximo})
	otra := i.Buscar(Consulta{Limite: LimiteMaximo})
	for k := range una.Normas {
		if una.Normas[k].ID != otra.Normas[k].ID {
			t.Fatalf("en la posición %d salió %s y después %s", k, una.Normas[k].ID, otra.Normas[k].ID)
		}
	}
}

func TestPaginar(t *testing.T) {
	i := indiceDePrueba(t)
	todas := i.Buscar(Consulta{Limite: LimiteMaximo})
	if todas.Total < 10 {
		t.Skip("el fixture es muy chico para paginar")
	}
	primera := i.Buscar(Consulta{Limite: 5})
	segunda := i.Buscar(Consulta{Limite: 5, Desplazamiento: 5})

	if len(primera.Normas) != 5 || len(segunda.Normas) != 5 {
		t.Fatalf("páginas de %d y %d", len(primera.Normas), len(segunda.Normas))
	}
	if primera.Total != todas.Total || segunda.Total != todas.Total {
		t.Error("el total cambia según la página")
	}
	if !primera.Truncado {
		t.Error("la primera página no avisa que hay más")
	}
	// Las dos páginas no se pisan, y juntas son el principio de la lista.
	for k := 0; k < 5; k++ {
		if primera.Normas[k].ID != todas.Normas[k].ID {
			t.Errorf("página 1, posición %d: %s vs %s", k, primera.Normas[k].ID, todas.Normas[k].ID)
		}
		if segunda.Normas[k].ID != todas.Normas[5+k].ID {
			t.Errorf("página 2, posición %d: %s vs %s", k, segunda.Normas[k].ID, todas.Normas[5+k].ID)
		}
	}
}

// Pedir más allá del final devuelve una página vacía, no un error ni la
// última.
func TestPaginarMasAllaDelFinal(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{Desplazamiento: 100000})
	if len(r.Normas) != 0 {
		t.Errorf("devolvió %d normas", len(r.Normas))
	}
	if r.Total == 0 {
		t.Error("perdió el total")
	}
}

// El límite tiene techo: sin él una consulta sin filtros devolvería las 81
// mil de una.
func TestElLimiteTieneTecho(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{Limite: 100000})
	if len(r.Normas) > LimiteMaximo {
		t.Errorf("devolvió %d normas", len(r.Normas))
	}
}

func TestNormaPorID(t *testing.T) {
	i := indiceDePrueba(t)
	alguna := i.Buscar(Consulta{Limite: 1}).Normas[0]

	n, hay := i.Norma(alguna.ID)
	if !hay || n.ID != alguna.ID {
		t.Fatalf("no se encontró %s", alguna.ID)
	}
	// En minúsculas y con espacios también.
	if _, hay := i.Norma("  " + strings.ToLower(alguna.ID) + "  "); !hay {
		t.Error("no se encontró con el identificador en minúsculas")
	}
	if _, hay := i.Norma("LPX9999999"); hay {
		t.Error("encontró una norma que no existe")
	}
}

func TestConteos(t *testing.T) {
	i := indiceDePrueba(t)
	porProv := i.PorProvincia()
	var suma int
	for _, c := range porProv {
		suma += c
	}
	if suma != i.Normas() {
		t.Errorf("las provincias suman %d y hay %d normas", suma, i.Normas())
	}

	tipos := i.Tipos()
	suma = 0
	for _, t := range tipos {
		suma += t.Normas
	}
	if suma != i.Normas() {
		t.Errorf("los tipos suman %d y hay %d normas", suma, i.Normas())
	}
	// Del más frecuente al menos.
	for k := 1; k < len(tipos); k++ {
		if tipos[k-1].Normas < tipos[k].Normas {
			t.Errorf("los tipos no están ordenados: %v", tipos)
			break
		}
	}
}

// Buscar en un índice vacío no puede explotar: es lo que pasa antes de la
// primera sincronización.
func TestBuscarSinCatalogo(t *testing.T) {
	i := NuevoIndice()
	r := i.Buscar(Consulta{Texto: "algo"})
	if r == nil || r.Total != 0 || len(r.Normas) != 0 {
		t.Errorf("resultado = %+v", r)
	}
	if _, hay := i.Norma("LPB1000000"); hay {
		t.Error("encontró algo en un índice vacío")
	}
}

// Lo que se llama así tiene que ir antes que lo que lo menciona de paso.
// Buscar "constitución salta" devolvía primero leyes de expropiación de 2021
// que nombran la Constitución en sus materias, y la Constitución de Salta
// quedaba enterrada por ser más vieja.
func TestLoQueSeLlamaAsiVaPrimero(t *testing.T) {
	normas := []Norma{
		{ID: "LPA0008286", Provincia: "Salta", ProvinciaID: "66", Tipo: "Ley", Numero: "8286",
			Fecha: "2021-11-04", TituloResumido: "Expropiación de inmueble en La Viña",
			TituloSumario: "EXPROPIACION-CONSTITUCION PROVINCIAL"},
		{ID: "LPA0000000", Provincia: "Salta", ProvinciaID: "66", Tipo: "Constitución Provincial",
			Numero: "0", Fecha: "1998-04-07", TituloResumido: "CONSTITUCION DE LA PROVINCIA DE SALTA"},
		{ID: "LPA0007000", Provincia: "Salta", ProvinciaID: "66", Tipo: "Ley", Numero: "7000",
			Fecha: "2020-01-01", TituloSumario: "CONSTITUCION PROVINCIAL-OTRA COSA"},
	}
	i := NuevoIndice()
	i.Reemplazar(normas)

	r := i.Buscar(Consulta{Texto: "constitución salta"})
	if r.Total < 2 {
		t.Fatalf("total = %d", r.Total)
	}
	if r.Normas[0].ID != "LPA0000000" {
		t.Errorf("primero salió %s (%s, %s); tendría que salir la Constitución",
			r.Normas[0].ID, r.Normas[0].Titulo(), r.Normas[0].Fecha)
	}
}

// Sin términos de búsqueda manda la fecha y nada más: la afinidad no puede
// desordenar un listado.
func TestSinTextoMandaLaFecha(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{Limite: LimiteMaximo})
	for k := 1; k < len(r.Normas); k++ {
		if r.Normas[k-1].Fecha < r.Normas[k].Fecha {
			t.Fatalf("desordenadas en la posición %d", k)
		}
	}
}

func TestPesoDe(t *testing.T) {
	consti := Norma{Tipo: "Constitución Provincial", Provincia: "Salta", Numero: "0",
		TituloResumido: "CONSTITUCION DE LA PROVINCIA DE SALTA"}
	ley := Norma{Tipo: "Ley", Provincia: "Salta", Numero: "8286",
		TituloResumido: "Expropiación de inmueble", TituloSumario: "EXPROPIACION-CONSTITUCION PROVINCIAL"}

	if a, b := pesoDe(consti, []string{"constitucion", "salta"}), pesoDe(ley, []string{"constitucion", "salta"}); a <= b {
		t.Errorf("la constitución pesa %d y la ley que la menciona %d", a, b)
	}
	// Buscar por número tiene que encontrar la ley.
	if pesoDe(ley, []string{"ley", "8286"}) == 0 {
		t.Error("buscar por tipo y número no le pega a la norma")
	}
}

// Buscar "agua" traía primero un presupuesto de la localidad de Bagual, que
// la contiene adentro. Lo que arranca una palabra tiene que ir antes.
func TestNoGanaLaCoincidenciaPorAdentro(t *testing.T) {
	i := NuevoIndice()
	i.Reemplazar([]Norma{
		{ID: "LPD0001188", Provincia: "San Luis", ProvinciaID: "74", Tipo: "Ley", Numero: "1188",
			Fecha: "2026-04-14", TituloResumido: "Presupuestos municipales de Saladillo y Bagual"},
		{ID: "LPK0005932", Provincia: "Catamarca", ProvinciaID: "10", Tipo: "Ley", Numero: "5932",
			Fecha: "2000-01-01", TituloResumido: "Acuerdo con Yacimientos Mineros de Agua de Dionisio"},
	})
	r := i.Buscar(Consulta{Texto: "agua"})
	if r.Total != 2 {
		t.Fatalf("total = %d; las dos la contienen", r.Total)
	}
	if r.Normas[0].ID != "LPK0005932" {
		t.Errorf("primero salió %q; tendría que ir la que la nombra de verdad",
			r.Normas[0].Titulo())
	}
}

// Pero el plural y los derivados se siguen encontrando: en castellano son lo
// normal, y perderlos sería peor que el problema que se arregla.
func TestElPluralYLosDerivadosSiguenValiendo(t *testing.T) {
	i := NuevoIndice()
	i.Reemplazar([]Norma{
		{ID: "LPB0000001", Provincia: "Buenos Aires", ProvinciaID: "06", Tipo: "Ley",
			Fecha: "2020-01-01", TituloResumido: "Régimen de aguas provinciales"},
		{ID: "LPB0000002", Provincia: "Buenos Aires", ProvinciaID: "06", Tipo: "Ley",
			Fecha: "2019-01-01", TituloResumido: "Infraestructura educacional"},
	})
	if r := i.Buscar(Consulta{Texto: "agua"}); r.Total != 1 {
		t.Errorf(`"agua" no encontró "aguas": total = %d`, r.Total)
	}
	if r := i.Buscar(Consulta{Texto: "educacion"}); r.Total != 1 {
		t.Errorf(`"educacion" no encontró "educacional": total = %d`, r.Total)
	}
}

func TestEmpiezaPalabraCon(t *testing.T) {
	casos := []struct {
		texto, termino string
		esperado       bool
	}{
		{"agua potable", "agua", true},
		{"aguas provinciales", "agua", true}, // el plural arranca igual
		{"regimen de aguas", "agua", true},
		{"presupuesto de bagual", "agua", false}, // adentro de otra palabra
		{"managua", "agua", false},
		{"ley 6109 de chaco", "6109", true},
		{"ley 16109", "6109", false},
		{"", "agua", false},
		{"agua", "agua", true},
	}
	for _, c := range casos {
		if got := empiezaPalabraCon(c.texto, c.termino); got != c.esperado {
			t.Errorf("%q en %q -> %v, se esperaba %v", c.termino, c.texto, got, c.esperado)
		}
	}
}
