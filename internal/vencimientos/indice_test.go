package vencimientos

import "testing"

func indiceDePrueba(t *testing.T) *Indice {
	t.Helper()
	rs, _, _ := Comparar(nil, agendaDePrueba(t), t0)
	ix := NuevoIndice()
	ix.Reemplazar(rs)
	return ix
}

// Nadie escribe los acentos en una caja de búsqueda.
func TestSeBuscaSinAcentos(t *testing.T) {
	res := indiceDePrueba(t).Buscar(Consulta{Texto: "electronica"})
	if res.Total == 0 {
		t.Fatal("«electronica» no encontró «electrónica»")
	}
}

// Lo que vence para todos también le vence a cada terminación.
func TestLaTerminacionTraeLoQueVenceParaTodos(t *testing.T) {
	for _, r := range indiceDePrueba(t).Buscar(Consulta{Terminacion: "5", Limite: LimiteMaximo}).Vencimientos {
		if !LeToca(r.TerminacionCUIT, "5") {
			t.Errorf("la terminación 5 trajo el grupo %q", r.TerminacionCUIT)
		}
	}
	if !LeToca("todos", "7") || LeToca("0-1-2-3", "4") || LeToca("10", "1") {
		t.Error("LeToca compara mal")
	}
}

// Una obligación sin fecha no vence en septiembre: se pide aparte.
func TestLasSinFechaSePidenAparte(t *testing.T) {
	ix := indiceDePrueba(t)
	for _, r := range ix.Buscar(Consulta{Desde: "2026-01-01", Hasta: "2026-12-31", Limite: LimiteMaximo}).Vencimientos {
		if r.SinFechaFija {
			t.Error("una sin fecha fija apareció en una consulta por fechas")
		}
	}
	if ix.Buscar(Consulta{SinFechaFija: true}).Total != 1 {
		t.Error("la de plazo relativo no se puede pedir")
	}
}

// Una lista recortada tiene que decirlo: si no, parece completa.
func TestUnaPaginaDiceQueHayMas(t *testing.T) {
	ix := indiceDePrueba(t)
	todo := ix.Buscar(Consulta{Limite: LimiteMaximo})
	pagina := ix.Buscar(Consulta{Limite: 2})
	if len(pagina.Vencimientos) != 2 || !pagina.Truncado || pagina.Total != todo.Total {
		t.Fatalf("página: %d de %d, hay_mas=%v", len(pagina.Vencimientos), pagina.Total, pagina.Truncado)
	}
	segunda := ix.Buscar(Consulta{Limite: 2, Desplazamiento: 2})
	if segunda.Vencimientos[0].Clave == pagina.Vencimientos[0].Clave {
		t.Error("la segunda página repite la primera")
	}
}

func TestElImpuestoSeComparaSinAcentosNiMayusculas(t *testing.T) {
	ix := indiceDePrueba(t)
	if ix.Buscar(Consulta{Impuesto: "impuesto a las ganancias"}).Total == 0 {
		t.Error("el filtro por impuesto distingue mayúsculas")
	}
}

func TestElConteoPorImpuestoSumaLosDelCatalogo(t *testing.T) {
	ix := indiceDePrueba(t)
	conteo := ix.PorImpuesto([]Impuesto{{Codigo: "16", Nombre: "IMPUESTO A LAS GANANCIAS"}, {Codigo: "45", Nombre: "APORTE SOLIDARIO Y EXTRAORDINARIO"}})
	var ganancias, aporte bool
	for _, c := range conteo {
		if c.Nombre == "IMPUESTO A LAS GANANCIAS" && c.Vencimientos > 0 {
			ganancias = true
		}
		if c.Nombre == "APORTE SOLIDARIO Y EXTRAORDINARIO" && c.Vencimientos == 0 {
			aporte = true
		}
	}
	if !ganancias || !aporte {
		t.Errorf("conteo: %+v", conteo)
	}
}
