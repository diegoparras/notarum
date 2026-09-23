package vencimientos

import (
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, nombre string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + nombre)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func agendaDePrueba(t *testing.T) Agenda {
	t.Helper()
	ag, err := Parsear(fixture(t, "agenda.html"),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Parsear: %v", err)
	}
	return ag
}

// La página viene en ISO-8859-1: "electrónica" viaja con el byte 0xF3.
func TestLosAcentosLleganEnteros(t *testing.T) {
	var visto bool
	for _, v := range agendaDePrueba(t).Vencimientos {
		if strings.Contains(v.Obligacion, "electrónica") || strings.Contains(v.Sujeto, "país") {
			visto = true
		}
		for _, c := range []string{v.Impuesto, v.Regimen, v.Sujeto, v.Obligacion, v.Periodo} {
			if strings.ContainsRune(c, '�') {
				t.Errorf("basura de codificación en %q", c)
			}
		}
	}
	if !visto {
		t.Error("ninguna palabra con acento llegó bien: el cuerpo no se leyó como ISO-8859-1")
	}
}

// Dos obligaciones que vencen igual se publican con una sola tabla de fechas.
// Aparearlas una a una deja a la segunda sin fecha o con la de otra.
func TestUnaTablaDeFechasSirveALasObligacionesQueLaPreceden(t *testing.T) {
	porObligacion := map[string]string{}
	for _, v := range agendaDePrueba(t).Vencimientos {
		if !v.SinFechaFija {
			porObligacion[v.Impuesto+"·"+v.Obligacion] += v.TerminacionCUIT + "=" + v.Fecha + ";"
		}
	}
	compartidas := map[string]int{}
	for _, fechas := range porObligacion {
		compartidas[fechas]++
	}
	var hay bool
	for _, n := range compartidas {
		if n > 1 {
			hay = true
		}
	}
	if !hay {
		t.Fatalf("ninguna tabla quedó compartida y la muestra tiene el caso: %v", porObligacion)
	}
}

// El IVA de prestaciones del exterior vence "dentro de los 10 días hábiles
// siguientes": no hay fecha de almanaque, y tirarlo sería decir que no existe.
func TestLoQueNoTieneFechaFijaSeGuarda(t *testing.T) {
	var n int
	for _, v := range agendaDePrueba(t).Vencimientos {
		if v.SinFechaFija {
			n++
			if v.Fecha != "" || v.TerminacionCUIT != "" || v.Obligacion == "" {
				t.Errorf("sin fecha fija mal armado: %+v", v)
			}
		}
	}
	if n != 1 {
		t.Errorf("se esperaba una obligación sin fecha fija, hubo %d", n)
	}
}

func TestLasFechasSalenEnISO(t *testing.T) {
	for _, v := range agendaDePrueba(t).Vencimientos {
		if v.SinFechaFija {
			continue
		}
		if _, err := time.Parse(FormatoISO, v.Fecha); err != nil || !strings.HasPrefix(v.Fecha, "2026-09-") {
			t.Errorf("fecha %q en %q", v.Fecha, v.Impuesto)
		}
		if v.TerminacionCUIT == "" {
			t.Errorf("fila con fecha y sin terminación: %+v", v)
		}
	}
}

// La fecha no es parte de la identidad: si lo fuera, una prórroga se vería
// como un vencimiento nuevo y otro que desapareció.
func TestLaClaveNoIncluyeLaFecha(t *testing.T) {
	v := agendaDePrueba(t).Vencimientos[0]
	corrido := v
	corrido.Fecha = "2026-12-31"
	if v.Clave() != corrido.Clave() {
		t.Error("mover la fecha cambió la clave")
	}
	otra := v
	otra.TerminacionCUIT += "-x"
	if v.Clave() == otra.Clave() {
		t.Error("dos terminaciones comparten clave")
	}
}

func TestNoHayClavesRepetidas(t *testing.T) {
	vistas := map[string]bool{}
	for _, v := range agendaDePrueba(t).Vencimientos {
		if vistas[v.Clave()] {
			t.Errorf("clave repetida: %+v", v)
		}
		vistas[v.Clave()] = true
	}
}

func TestLasNormasTraenSuEnlace(t *testing.T) {
	var con int
	for _, v := range agendaDePrueba(t).Vencimientos {
		for _, n := range v.Normas {
			if n.Texto == "" || (n.URL != "" && !strings.HasPrefix(n.URL, "http")) {
				t.Errorf("norma rara: %+v", n)
			}
		}
		if len(v.Normas) > 0 {
			con++
		}
	}
	if con == 0 {
		t.Error("ninguna fila trajo normas y la muestra las tiene")
	}
}

// El formulario contesta 200 también con la lista de errores. Tomarlo como una
// agenda vacía sería decir que no vence nada.
func TestUnaPaginaDeErrorNoEsUnaAgendaVacia(t *testing.T) {
	_, err := Parsear(fixture(t, "formulario.html"), time.Time{}, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "impuesto") {
		t.Fatalf("la página de errores no se reconoció: %v", err)
	}
}

func TestElRangoNoCargadoSeDistingue(t *testing.T) {
	pagina := `<html><h3><font color="red">Lista de Errores</font></h3><ul><li>se ingresó un rango de fecha que no está cargado</li></ul></html>`
	_, err := Parsear(aLatin1(pagina), time.Time{}, time.Time{})
	if !errorsIs(err, ErrRangoNoCargado) {
		t.Fatalf("no se distinguió el rango no cargado: %v", err)
	}
}

func TestElCatalogoSaleDelDesplegable(t *testing.T) {
	cat, err := ParsearCatalogo(fixture(t, "formulario.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cat) < 30 {
		t.Errorf("%d impuestos", len(cat))
	}
	var iva bool
	for _, i := range cat {
		if i.Codigo == CodigoTodos || i.Nombre == "-" {
			t.Errorf("entrada que no es impuesto: %+v", i)
		}
		iva = iva || i.Nombre == "IMPUESTO AL VALOR AGREGADO"
	}
	if !iva {
		t.Error("falta el IVA")
	}
}

func TestLaTerminacionSaleDelCUIT(t *testing.T) {
	casos := map[string]string{"": "", "4": "4", "30-71234567-4": "4", "20123456789": "9"}
	for entrada, esperado := range casos {
		got, err := TerminacionDeCUIT(entrada)
		if err != nil || got != esperado {
			t.Errorf("%q → %q, %v; se esperaba %q", entrada, got, err, esperado)
		}
	}
	for _, malo := range []string{"30-12", "abc", "123"} {
		if _, err := TerminacionDeCUIT(malo); err == nil {
			t.Errorf("%q se aceptó", malo)
		}
	}
}

// aLatin1 codifica como la página real: ISO-8859-1, un byte por letra.
func aLatin1(s string) []byte {
	var b []byte
	for _, r := range s {
		b = append(b, byte(r))
	}
	return b
}
