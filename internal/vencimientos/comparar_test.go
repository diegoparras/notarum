package vencimientos

import (
	"errors"
	"testing"
	"time"
)

func errorsIs(err, target error) bool { return errors.Is(err, target) }

func errorsAs(err error, target any) bool { return errors.As(err, target) }

var (
	t0 = time.Date(2026, 9, 20, 5, 0, 0, 0, time.UTC)
	t1 = t0.Add(24 * time.Hour)
	t2 = t1.Add(24 * time.Hour)
)

func venc(obligacion, fecha string) Vencimiento {
	v := Vencimiento{Impuesto: "IVA", Obligacion: obligacion, Periodo: "Agosto/2026", TerminacionCUIT: "0-1-2-3", Fecha: fecha}
	if fecha == "" {
		v.SinFechaFija, v.TerminacionCUIT = true, ""
	}
	return v
}

func agenda(vs ...Vencimiento) Agenda {
	return Agenda{
		Desde: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), Hasta: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		Vencimientos: vs,
	}
}

func buscarCambio(cs []Cambio, tipo string) (Cambio, bool) {
	for _, c := range cs {
		if c.Tipo == tipo {
			return c, true
		}
	}
	return Cambio{}, false
}

// La primera bajada no es noticia: todo es nuevo.
func TestLaCargaInicialNoRegistraAltas(t *testing.T) {
	rs, cs, res := Comparar(nil, agenda(venc("DDJJ", "2026-09-18"), venc("Pago", "2026-09-19")), t0)
	if len(rs) != 2 || len(cs) != 0 || !res.CargaInicial {
		t.Fatalf("carga inicial: %d registros, %d cambios, %+v", len(rs), len(cs), res)
	}
}

// Es la razón del modelo: ARCA corre una fecha y queda registrado con las dos.
func TestUnaProrrogaEsUnCambioSobreElMismoVencimiento(t *testing.T) {
	rs, _, _ := Comparar(nil, agenda(venc("DDJJ", "2026-09-18")), t0)
	rs, cs, res := Comparar(rs, agenda(venc("DDJJ", "2026-09-22")), t1)

	if len(rs) != 1 {
		t.Fatalf("la prórroga dejó %d registros", len(rs))
	}
	c, hay := buscarCambio(cs, Prorroga)
	if !hay || res.Prorrogas != 1 {
		t.Fatalf("no se registró la prórroga: %+v %+v", cs, res)
	}
	if c.FechaAnterior != "2026-09-18" || c.FechaNueva != "2026-09-22" || c.Dias != 4 {
		t.Errorf("prórroga mal registrada: %+v", c)
	}
	if !c.VistoAnterior.Equal(t0) || !c.DetectadoEn.Equal(t1) {
		t.Errorf("la ventana del cambio no son las dos bajadas: %+v", c)
	}
	if !rs[0].FechaDesde.Equal(t1) || !rs[0].VistoPrimero.Equal(t0) {
		t.Errorf("fechas del registro: %+v", rs[0])
	}
}

func TestUnAdelantoSeDistingueDeUnaProrroga(t *testing.T) {
	rs, _, _ := Comparar(nil, agenda(venc("DDJJ", "2026-09-18")), t0)
	_, cs, res := Comparar(rs, agenda(venc("DDJJ", "2026-09-15")), t1)
	if c, hay := buscarCambio(cs, Adelanto); !hay || c.Dias != -3 || res.Adelantos != 1 {
		t.Fatalf("adelanto: %+v %+v", cs, res)
	}
}

// Si nada cambia, "desde cuándo rige esta fecha" no se mueve: pisarlo en cada
// bajada lo convertiría en "desde esta mañana".
func TestUnaBajadaIgualNoMueveNada(t *testing.T) {
	rs, _, _ := Comparar(nil, agenda(venc("DDJJ", "2026-09-18")), t0)
	rs, cs, res := Comparar(rs, agenda(venc("DDJJ", "2026-09-18")), t1)
	if len(cs) != 0 || res.Novedades() != 0 {
		t.Fatalf("una bajada igual registró cambios: %+v", cs)
	}
	if !rs[0].FechaDesde.Equal(t0) || !rs[0].VistoUltimo.Equal(t1) {
		t.Errorf("registro: %+v", rs[0])
	}
}

// Que un vencimiento de 2027 no venga en una consulta por 2026 no dice nada de
// él. Sólo se da de baja lo que falta adentro de la ventana.
func TestLaBajaEsSoloAdentroDeLaVentana(t *testing.T) {
	rs, _, _ := Comparar(nil, agenda(venc("DDJJ", "2026-09-18"), venc("Anual", "2027-05-10"), venc("Otra", "2026-10-01")), t0)
	rs, cs, res := Comparar(rs, agenda(venc("Otra", "2026-10-01")), t1)

	if res.Bajas != 1 {
		t.Fatalf("bajas = %d: %+v", res.Bajas, cs)
	}
	for _, r := range rs {
		switch r.Obligacion {
		case "DDJJ":
			if r.Vigente() {
				t.Error("lo que faltó adentro de la ventana sigue vigente")
			}
		case "Anual":
			if !r.Vigente() {
				t.Error("se dio de baja algo de afuera de la ventana")
			}
		}
	}
	if len(rs) != 3 {
		t.Errorf("una baja borró el registro: quedan %d", len(rs))
	}
}

func TestLoDadoDeBajaQueVuelveQuedaRegistrado(t *testing.T) {
	rs, _, _ := Comparar(nil, agenda(venc("DDJJ", "2026-09-18"), venc("Otra", "2026-10-01")), t0)
	rs, _, _ = Comparar(rs, agenda(venc("Otra", "2026-10-01")), t1)
	rs, cs, res := Comparar(rs, agenda(venc("DDJJ", "2026-09-18"), venc("Otra", "2026-10-01")), t2)
	if _, hay := buscarCambio(cs, Reaparicion); !hay || res.Altas != 0 {
		t.Fatalf("la reaparición no se registró: %+v %+v", cs, res)
	}
	for _, r := range rs {
		if !r.Vigente() {
			t.Errorf("%s sigue retirado", r.Obligacion)
		}
	}
}

func TestUnAltaDespuesDeLaCargaInicialEsNoticia(t *testing.T) {
	rs, _, _ := Comparar(nil, agenda(venc("DDJJ", "2026-09-18")), t0)
	_, cs, res := Comparar(rs, agenda(venc("DDJJ", "2026-09-18"), venc("Nueva", "2026-11-05")), t1)
	if c, hay := buscarCambio(cs, Alta); !hay || c.FechaNueva != "2026-11-05" || res.Altas != 1 {
		t.Fatalf("alta: %+v %+v", cs, res)
	}
}

func TestLaAgendaQuedaOrdenadaPorFechaYLasSinFechaAlFinal(t *testing.T) {
	rs, _, _ := Comparar(nil, agenda(venc("C", ""), venc("B", "2026-10-01"), venc("A", "2026-09-18")), t0)
	if rs[0].Fecha != "2026-09-18" || rs[1].Fecha != "2026-10-01" || !rs[2].SinFechaFija {
		t.Errorf("orden: %v %v %v", rs[0].Fecha, rs[1].Fecha, rs[2].SinFechaFija)
	}
}
