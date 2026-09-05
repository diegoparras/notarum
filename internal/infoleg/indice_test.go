package infoleg

import "testing"

func indiceDePrueba(t *testing.T) *Indice {
	t.Helper()
	i := NuevoIndiceCon(10)
	internar := Internador()
	for _, n := range []Norma{
		{ID: 638, Tipo: "Ley", Numero: "24240", FechaSancion: "1993-09-22",
			TituloResumido: "REGIMEN LEGAL", TituloSumario: "DEFENSA DEL CONSUMIDOR-CONTRATOS",
			TieneTexto: true},
		{ID: 262686, Tipo: "Ley", Numero: "27250", FechaSancion: "2016-05-18",
			TituloResumido: "LEY Nº 24240 - MODIFICACION", TieneTexto: true},
		{ID: 17723, Tipo: "Decreto", Numero: "2089", FechaSancion: "1993-10-13",
			TituloResumido: "OBSERVACION PARCIAL Y PROMULGACION"},
		{ID: 900, Tipo: "Resolución", Numero: "45", FechaSancion: "2020-03-01",
			TituloSumario: "EDUCACION-ESCUELAS"},
	} {
		i.Agregar(n, internar)
	}
	i.Cerrar()
	return i
}

func TestBuscarNacional(t *testing.T) {
	i := indiceDePrueba(t)
	if i.Normas() != 4 || !i.Cargado() {
		t.Fatalf("normas = %d", i.Normas())
	}
	r := i.Buscar(Consulta{Texto: "consumidor"})
	if r.Total != 1 || r.Normas[0].ID != 638 {
		t.Errorf("buscar por materia = %+v", r.Normas)
	}
}

// Buscar una norma por su número tiene que traerla a ella, no a las que la
// nombran. Pasó con "ley 24240": primero salía la 27250, que la modifica.
func TestElNumeroExactoIdentificaLaNorma(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{Texto: "ley 24240"})
	if len(r.Normas) == 0 {
		t.Fatal("no encontró nada")
	}
	if r.Normas[0].ID != 638 {
		t.Errorf("primera salió %d (%s %s); tendría que ser la 24240 misma",
			r.Normas[0].ID, r.Normas[0].Tipo, r.Normas[0].Numero)
	}
	// Y con puntos, que es como se escribe.
	r2 := i.Buscar(Consulta{Texto: "24.240"})
	if len(r2.Normas) == 0 || r2.Normas[0].ID != 638 {
		t.Errorf("con puntos no la encontró primero: %+v", r2.Normas)
	}
}

func TestNormalizarNumero(t *testing.T) {
	for entrada, esperado := range map[string]string{
		"24240": "24240", "24.240": "24240", "024240": "24240",
		"0": "", "": "", "ley": "", "24240-b": "",
	} {
		if got := normalizarNumero(entrada); got != esperado {
			t.Errorf("%q -> %q, se esperaba %q", entrada, got, esperado)
		}
	}
}

func TestFiltrosNacionales(t *testing.T) {
	i := indiceDePrueba(t)
	if r := i.Buscar(Consulta{Tipo: "Ley"}); r.Total != 2 {
		t.Errorf("por tipo = %d", r.Total)
	}
	if r := i.Buscar(Consulta{Desde: 2000}); r.Total != 2 {
		t.Errorf("desde 2000 = %d", r.Total)
	}
	if r := i.Buscar(Consulta{Hasta: 1999}); r.Total != 2 {
		t.Errorf("hasta 1999 = %d", r.Total)
	}
	if r := i.Buscar(Consulta{SoloConTexto: true}); r.Total != 2 {
		t.Errorf("con texto = %d", r.Total)
	}
}

// Sin título se muestra la primera materia: una norma sin nada que mostrar es
// una fila en blanco en la lista.
func TestSiempreHayQueMostrar(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{Texto: "educacion"})
	if len(r.Normas) != 1 {
		t.Fatalf("normas = %d", len(r.Normas))
	}
	if r.Normas[0].Titulo == "" {
		t.Error("la norma sin título quedó sin nada que mostrar")
	}
}

func TestElOrdenNacionalEsPorFecha(t *testing.T) {
	i := indiceDePrueba(t)
	r := i.Buscar(Consulta{Limite: LimiteMaximo})
	for k := 1; k < len(r.Normas); k++ {
		if r.Normas[k-1].Fecha < r.Normas[k].Fecha {
			t.Fatalf("desordenadas en %d", k)
		}
	}
}

func TestPaginarNacional(t *testing.T) {
	i := indiceDePrueba(t)
	todas := i.Buscar(Consulta{Limite: LimiteMaximo})
	una := i.Buscar(Consulta{Limite: 2})
	dos := i.Buscar(Consulta{Limite: 2, Desplazamiento: 2})

	if una.Total != todas.Total || dos.Total != todas.Total {
		t.Error("el total cambia entre páginas")
	}
	if !una.Truncado {
		t.Error("la primera página no avisa que hay más")
	}
	if len(una.Normas) != 2 || len(dos.Normas) != 2 {
		t.Errorf("páginas de %d y %d", len(una.Normas), len(dos.Normas))
	}
	if una.Normas[0].ID == dos.Normas[0].ID {
		t.Error("las dos páginas empiezan igual")
	}
}

func TestElLimiteNacionalTieneTecho(t *testing.T) {
	i := indiceDePrueba(t)
	if r := i.Buscar(Consulta{Limite: 100000}); len(r.Normas) > LimiteMaximo {
		t.Errorf("devolvió %d", len(r.Normas))
	}
}

// Buscar en un índice vacío no puede explotar: es lo que pasa antes de
// sincronizar.
func TestBuscarSinCatalogoNacional(t *testing.T) {
	i := NuevoIndice()
	if i.Cargado() {
		t.Error("un índice vacío dice estar cargado")
	}
	r := i.Buscar(Consulta{Texto: "algo"})
	if r == nil || r.Total != 0 {
		t.Errorf("resultado = %+v", r)
	}
	if len(i.Tipos()) != 0 {
		t.Error("hay tipos en un índice vacío")
	}
}

func TestInternador(t *testing.T) {
	internar := Internador()
	a, b := internar("Resolución"), internar("Resolución")
	if a != b {
		t.Error("el internador devolvió cadenas distintas")
	}
	if internar("") != "" {
		t.Error("el internador tocó la cadena vacía")
	}
}

func TestTiposNacionales(t *testing.T) {
	i := indiceDePrueba(t)
	tipos := i.Tipos()
	var suma int
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
