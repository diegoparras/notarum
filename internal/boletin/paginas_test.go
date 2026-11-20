package boletin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// La portada anuncia, en su índice de rubros, cuántos avisos tiene la edición
// entera. Es contra lo que se sabe si lo extraído está completo.
func TestParsearPortadaLeeLoQueDeclaraElSitio(t *testing.T) {
	casos := []struct {
		fixture    string
		seccion    Seccion
		fecha      string
		cantidad   int
		declarados int
		parcial    bool
	}{
		// Medido contra el sitio el 19/11/2026: la Segunda del 1/9 tiene 410
		// avisos y la Primera 129. La portada sola trae los primeros 100.
		{"portada_segunda_20260901.html", Segunda, "2026-09-01", 100, 410, true},
		{"portada_primera_20260901.html", Primera, "2026-09-01", 100, 129, true},
		{"portada_tercera_20260901.html", Tercera, "2026-09-01", 54, 54, false},
		{"portada_primera_20260715.html", Primera, "2026-07-15", 73, 73, false},
		{"portada_primera_20250310.html", Primera, "2025-03-10", 52, 52, false},
		{"portada_primera_suplemento_20260814.html", Primera, "2026-08-14", 21, 21, false},
	}
	for _, c := range casos {
		t.Run(c.fixture, func(t *testing.T) {
			fecha, _ := ParseFecha(c.fecha)
			ed, err := ParsearPortada(fixture(t, c.fixture), c.seccion, fecha)
			if err != nil {
				t.Fatal(err)
			}
			if ed.Cantidad != c.cantidad || ed.Declarados != c.declarados || ed.Parcial != c.parcial {
				t.Errorf("cantidad=%d declarados=%d parcial=%v; se esperaba %d, %d, %v",
					ed.Cantidad, ed.Declarados, ed.Parcial, c.cantidad, c.declarados, c.parcial)
			}
		})
	}
}

// Una página que sigue dentro del mismo rubro no repite el encabezado: el
// rubro viene arrastrado de la página anterior.
func TestParsearPaginaDeSeccionArrastraElRubro(t *testing.T) {
	const rubro = "CONVOCATORIAS Y AV.COMERCIALES - AVISOS COMERCIALES"
	p, err := parsearPaginaDeSeccion(fixture(t, "seccion_segunda_20260901_p3.json"), Segunda, rubro)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.avisos) != 100 {
		t.Fatalf("avisos = %d, se esperaban 100", len(p.avisos))
	}
	if p.avisos[0].Rubro != rubro {
		t.Errorf("rubro del primero = %q, se esperaba el arrastrado", p.avisos[0].Rubro)
	}
	if !p.hayMas || p.ultimoRubro == "" {
		t.Errorf("hayMas=%v ultimoRubro=%q", p.hayMas, p.ultimoRubro)
	}
	for _, a := range p.avisos {
		if a.Rubro == "" {
			t.Fatalf("el aviso %s quedó sin rubro", a.ID)
		}
	}

	vacia, err := parsearPaginaDeSeccion(fixture(t, "seccion_segunda_20260901_p6.json"), Segunda, rubro)
	if err != nil {
		t.Fatal(err)
	}
	if len(vacia.avisos) != 0 || vacia.hayMas {
		t.Errorf("la última página: avisos=%d hayMas=%v", len(vacia.avisos), vacia.hayMas)
	}
}

// sitioPaginado contesta como el Boletín: la portada deja una cookie de
// sesión, y /seccion/actualizar sólo sabe de qué fecha se habla por esa
// cookie. Sin ella entrega la edición del día, que es otra.
type sitioPaginado struct {
	t       *testing.T
	seccion string
	fecha   string
	portada []byte
	paginas map[string][]byte // por número de página
	pedidos int32
	rubros  []string // ult_rubro recibido en cada página pedida
	rompe   func(pag string, w http.ResponseWriter) bool
}

func (s *sitioPaginado) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&s.pedidos, 1)
	switch r.URL.Path {
	case "/seccion/" + s.seccion + "/" + s.fecha:
		http.SetCookie(w, &http.Cookie{Name: "BoletinWebSession", Value: "sesion-" + s.fecha, Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "BORA", Value: "nodo-1", Path: "/"})
		w.Write(s.portada)
	case "/seccion/actualizar/" + s.seccion:
		if c, err := r.Cookie("BoletinWebSession"); err != nil || c.Value != "sesion-"+s.fecha {
			s.t.Errorf("la página %s se pidió sin la cookie de sesión de la portada", r.URL.RawQuery)
			http.Error(w, "sin sesión", http.StatusInternalServerError)
			return
		}
		pag := r.URL.Query().Get("pag")
		s.rubros = append(s.rubros, r.URL.Query().Get("ult_rubro"))
		if s.rompe != nil && s.rompe(pag, w) {
			return
		}
		cuerpo, ok := s.paginas[pag]
		if !ok {
			cuerpo = []byte(`{"hay_mas_datos":false,"html":"","sig_pag":99,"ult_rubro":"","id_rubro":null}`)
		}
		w.Write(cuerpo)
	default:
		http.Error(w, "no", http.StatusNotFound)
	}
}

func segunda20260901(t *testing.T) *sitioPaginado {
	t.Helper()
	s := &sitioPaginado{
		t: t, seccion: "segunda", fecha: "20260901",
		portada: fixture(t, "portada_segunda_20260901.html"),
		paginas: map[string][]byte{},
	}
	for p := 2; p <= 6; p++ {
		s.paginas[fmt.Sprint(p)] = fixture(t, fmt.Sprintf("seccion_segunda_20260901_p%d.json", p))
	}
	return s
}

// El caso que motivó todo: la Segunda de un día hábil tiene cuatro veces lo
// que entra en la portada. Las páginas son las que entregó el sitio.
func TestTraerEdicionSigueLasPaginasDeLaSegunda(t *testing.T) {
	sitio := segunda20260901(t)
	c := clienteDePrueba(t, sitio)
	fecha, _ := ParseFecha("2026-09-01")

	ed, err := c.TraerEdicion(context.Background(), Segunda, fecha, "")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 410 || len(ed.Avisos) != 410 {
		t.Fatalf("cantidad = %d (avisos %d), se esperaban 410", ed.Cantidad, len(ed.Avisos))
	}
	if ed.Declarados != 410 || ed.Parcial {
		t.Errorf("declarados=%d parcial=%v", ed.Declarados, ed.Parcial)
	}
	if ed.Paginas != 5 {
		t.Errorf("paginas = %d, se esperaban 5", ed.Paginas)
	}
	// Con lo declarado alcanzado no hace falta pedir la sexta, que viene vacía.
	if sitio.pedidos != 5 {
		t.Errorf("pedidos al sitio = %d, se esperaban 5", sitio.pedidos)
	}
	if len(sitio.rubros) == 0 || sitio.rubros[0] != "CONVOCATORIAS Y AV.COMERCIALES - CONVOCATORIAS - ANTERIOR" {
		t.Errorf("ult_rubro de la página 2 = %q: tiene que ser el que deja la portada", sitio.rubros)
	}

	vistos := map[string]bool{}
	var suma int
	for _, a := range ed.Avisos {
		if vistos[a.ID] {
			t.Errorf("aviso repetido: %s", a.ID)
		}
		vistos[a.ID] = true
		if a.Rubro == "" || a.Organismo == "" {
			t.Errorf("aviso incompleto: %+v", a)
		}
	}
	for _, n := range ed.PorRubro {
		suma += n
	}
	if suma != 410 {
		t.Errorf("por_rubro suma %d", suma)
	}
	// Lo que faltaba: convocatorias, avisos comerciales y edictos judiciales.
	for rubro, n := range map[string]int{
		"CONVOCATORIAS Y AV.COMERCIALES - AVISOS COMERCIALES":                           123,
		"CONVOCATORIAS Y AV.COMERCIALES - AVISOS COMERCIALES - ANTERIOR":                1,
		"EDICTOS JUDICIALES - SUCESIONES":                                               103,
		"EDICTOS JUDICIALES - CITACIONES Y NOTIFICACIONES. CONCURSOS Y QUIEBRAS. OTROS": 19,
		"EDICTOS JUDICIALES - REMATES JUDICIALES - ANTERIOR":                            2,
		"SOCIEDADES ANONIMAS - CONSTITUCION SA":                                         18,
	} {
		if ed.PorRubro[rubro] != n {
			t.Errorf("por_rubro[%q] = %d, se esperaban %d", rubro, ed.PorRubro[rubro], n)
		}
	}
}

// A la Primera le pasa lo mismo el día que supera los 100.
func TestTraerEdicionSigueLasPaginasDeLaPrimera(t *testing.T) {
	sitio := &sitioPaginado{
		t: t, seccion: "primera", fecha: "20260901",
		portada: fixture(t, "portada_primera_20260901.html"),
		paginas: map[string][]byte{
			"2": fixture(t, "seccion_primera_20260901_p2.json"),
			"3": fixture(t, "seccion_primera_20260901_p3.json"),
		},
	}
	c := clienteDePrueba(t, sitio)
	fecha, _ := ParseFecha("2026-09-01")
	ed, err := c.TraerEdicion(context.Background(), Primera, fecha, "")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 129 || ed.Declarados != 129 || ed.Parcial {
		t.Errorf("cantidad=%d declarados=%d parcial=%v", ed.Cantidad, ed.Declarados, ed.Parcial)
	}
	if ed.PorRubro["AVISOS OFICIALES"] == 0 {
		t.Errorf("no llegaron los avisos oficiales, que estaban en la segunda página: %v", ed.PorRubro)
	}
}

// Si la portada ya trae todo lo que declara, no se le pide nada más al sitio.
func TestTraerEdicionNoPideDeMasSiLaPortadaAlcanza(t *testing.T) {
	sitio := &sitioPaginado{
		t: t, seccion: "tercera", fecha: "20260901",
		portada: fixture(t, "portada_tercera_20260901.html"),
	}
	c := clienteDePrueba(t, sitio)
	fecha, _ := ParseFecha("2026-09-01")
	ed, err := c.TraerEdicion(context.Background(), Tercera, fecha, "")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 54 || ed.Parcial || ed.Paginas != 1 {
		t.Errorf("cantidad=%d parcial=%v paginas=%d", ed.Cantidad, ed.Parcial, ed.Paginas)
	}
	if sitio.pedidos != 1 {
		t.Errorf("pedidos = %d, se esperaba 1", sitio.pedidos)
	}
}

// Una página que falla es una edición que no se pudo leer: es preferible el
// error a entregar una parte como si fuera el todo.
func TestTraerEdicionFallaSiUnaPaginaFalla(t *testing.T) {
	sitio := segunda20260901(t)
	sitio.rompe = func(pag string, w http.ResponseWriter) bool {
		if pag == "3" {
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	}
	c := clienteDePrueba(t, sitio)
	fecha, _ := ParseFecha("2026-09-01")
	ed, err := c.TraerEdicion(context.Background(), Segunda, fecha, "")
	if err == nil {
		t.Fatalf("se esperaba un error y vino una edición de %d avisos", ed.Cantidad)
	}
	var es *ErrDelSitio
	if !errors.As(err, &es) || es.Codigo != http.StatusTooManyRequests {
		t.Errorf("err = %v, se esperaba ErrDelSitio con código 429", err)
	}
}

// Si el sitio dice que no hay más antes de llegar a lo que él mismo declara,
// la edición sale, pero dice que es parcial.
func TestTraerEdicionMarcaParcialSiElSitioCortaAntes(t *testing.T) {
	sitio := segunda20260901(t)
	delete(sitio.paginas, "3") // la tercera contesta vacía y sin más datos
	c := clienteDePrueba(t, sitio)
	fecha, _ := ParseFecha("2026-09-01")
	ed, err := c.TraerEdicion(context.Background(), Segunda, fecha, "")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 200 || ed.Declarados != 410 || !ed.Parcial {
		t.Errorf("cantidad=%d declarados=%d parcial=%v; se esperaba 200, 410, true",
			ed.Cantidad, ed.Declarados, ed.Parcial)
	}
}

// Si la sesión se pierde, el sitio entrega las páginas de la edición del día.
// Mezclar dos fechas es un error, no una edición.
func TestTraerEdicionRechazaPaginasDeOtraFecha(t *testing.T) {
	sitio := segunda20260901(t)
	otra := strings.ReplaceAll(string(sitio.paginas["2"]), "20260901", "20261119")
	sitio.paginas["2"] = []byte(otra)
	c := clienteDePrueba(t, sitio)
	fecha, _ := ParseFecha("2026-09-01")
	if _, err := c.TraerEdicion(context.Background(), Segunda, fecha, ""); err == nil {
		t.Fatal("se esperaba error: la página 2 trae avisos de otra fecha")
	}
}

// Con filtro de rubro no se pagina, pero se sabe cuántos declara ese rubro.
func TestTraerEdicionConRubroComparaContraEseRubro(t *testing.T) {
	cuerpo := fixture(t, "portada_tercera_rubro1566_20260901.html")
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(cuerpo)
	}))
	fecha, _ := ParseFecha("2026-09-01")
	ed, err := c.TraerEdicion(context.Background(), Tercera, fecha, "1566")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 9 || ed.Declarados != 9 || ed.Parcial {
		t.Errorf("cantidad=%d declarados=%d parcial=%v", ed.Cantidad, ed.Declarados, ed.Parcial)
	}
}
