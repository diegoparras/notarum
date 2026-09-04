package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

func fixture(t *testing.T, nombre string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "boletin", "testdata", nombre))
	if err != nil {
		t.Fatalf("no se pudo leer el fixture %s: %v", nombre, err)
	}
	return b
}

func servidorDePrueba(t *testing.T, conIndice bool) *Servidor {
	t.Helper()
	portada := fixture(t, "portada_primera_20260901.html")
	detalle := fixture(t, "detalle_primera_346633.html")
	cal := fixture(t, "calendario_primera_2026.json")
	rubros := fixture(t, "rubros_primera.json")
	busq := fixture(t, "busqueda_primera.json")

	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/seccion/primera/20260901":
			w.Write(portada)
		case p == "/seccion/primera/20260817":
			http.Redirect(w, r, "/", http.StatusFound)
		case strings.HasPrefix(p, "/detalleAviso/"):
			w.Write(detalle)
		case strings.HasPrefix(p, "/calendario/"):
			w.Write(cal)
		case strings.HasSuffix(p, "/rubros"):
			w.Write(rubros)
		case p == "/busquedaAvanzada/realizarBusqueda":
			w.Write(busq)
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	t.Cleanup(origen.Close)

	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: origen.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
	})
	var alm almacen.Almacen
	var err error
	if conIndice {
		alm, err = almacen.NuevoSQLite(filepath.Join(t.TempDir(), "n.db"))
	} else {
		alm, err = almacen.NuevoDisco(t.TempDir())
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { alm.Cerrar() })
	return Nuevo(servicio.Nuevo(cli, alm), "test")
}

func llamar(t *testing.T, s *Servidor, metodo string, params any) *Respuesta {
	t.Helper()
	cuerpo := map[string]any{"jsonrpc": "2.0", "id": 1, "method": metodo}
	if params != nil {
		cuerpo["params"] = params
	}
	crudo, err := json.Marshal(cuerpo)
	if err != nil {
		t.Fatal(err)
	}
	return s.Atender(context.Background(), crudo)
}

func llamarHerramienta(t *testing.T, s *Servidor, nombre string, args map[string]any) *ResultadoHerramienta {
	t.Helper()
	res := llamar(t, s, "tools/call", map[string]any{"name": nombre, "arguments": args})
	if res == nil {
		t.Fatal("tools/call no devolvió respuesta")
	}
	if res.Error != nil {
		t.Fatalf("error de protocolo: %+v", res.Error)
	}
	r, ok := res.Result.(*ResultadoHerramienta)
	if !ok {
		t.Fatalf("el resultado no es una respuesta de herramienta: %T", res.Result)
	}
	return r
}

// El texto que devuelve una herramienta tiene que ser JSON que se pueda leer.
func decodificar(t *testing.T, r *ResultadoHerramienta, destino any) {
	t.Helper()
	if r.EsError {
		t.Fatalf("la herramienta devolvió error: %s", r.Contenido[0].Texto)
	}
	if len(r.Contenido) == 0 {
		t.Fatal("la herramienta no devolvió contenido")
	}
	if err := json.Unmarshal([]byte(r.Contenido[0].Texto), destino); err != nil {
		t.Fatalf("el contenido no es JSON: %v\n%s", err, r.Contenido[0].Texto)
	}
}

func TestInitialize(t *testing.T) {
	s := servidorDePrueba(t, false)
	res := llamar(t, s, "initialize", map[string]any{"protocolVersion": VersionProtocolo})
	if res == nil || res.Error != nil {
		t.Fatalf("res = %+v", res)
	}
	m, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("resultado = %T", res.Result)
	}
	if m["protocolVersion"] != VersionProtocolo {
		t.Errorf("protocolVersion = %v", m["protocolVersion"])
	}
	info := m["serverInfo"].(map[string]any)
	if info["name"] != "notarum" {
		t.Errorf("name = %v", info["name"])
	}
	if _, hay := m["instructions"]; !hay {
		t.Error("faltan las instrucciones para el modelo")
	}
}

// Una notificación no lleva respuesta: mandarla rompería el protocolo.
func TestNotificacionNoRespondeNada(t *testing.T) {
	s := servidorDePrueba(t, false)
	crudo := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if res := s.Atender(context.Background(), crudo); res != nil {
		t.Errorf("una notificación devolvió %+v", res)
	}
}

func TestListarHerramientas(t *testing.T) {
	s := servidorDePrueba(t, false)
	res := llamar(t, s, "tools/list", nil)
	if res == nil || res.Error != nil {
		t.Fatalf("res = %+v", res)
	}
	m := res.Result.(map[string]any)
	hs := m["tools"].([]Herramienta)
	if len(hs) < 6 {
		t.Fatalf("herramientas = %d", len(hs))
	}
	esperadas := map[string]bool{"edicion": false, "aviso": false, "buscar": false,
		"calendario": false, "rubros": false, "estado": false}
	for _, h := range hs {
		if _, hay := esperadas[h.Nombre]; hay {
			esperadas[h.Nombre] = true
		}
		if h.Descripcion == "" || h.Esquema == nil {
			t.Errorf("la herramienta %s está incompleta", h.Nombre)
		}
	}
	for nombre, vista := range esperadas {
		if !vista {
			t.Errorf("falta la herramienta %s", nombre)
		}
	}
}

func TestMetodoDesconocido(t *testing.T) {
	s := servidorDePrueba(t, false)
	res := llamar(t, s, "recursos/listar", nil)
	if res == nil || res.Error == nil {
		t.Fatalf("res = %+v", res)
	}
	if res.Error.Codigo != CodigoMetodoNoExiste {
		t.Errorf("codigo = %d", res.Error.Codigo)
	}
}

func TestMensajeRoto(t *testing.T) {
	s := servidorDePrueba(t, false)
	res := s.Atender(context.Background(), []byte(`{esto no es json`))
	if res == nil || res.Error == nil || res.Error.Codigo != CodigoParseo {
		t.Errorf("res = %+v", res)
	}
}

func TestHerramientaEdicion(t *testing.T) {
	s := servidorDePrueba(t, false)
	r := llamarHerramienta(t, s, "edicion", map[string]any{
		"seccion": "primera", "fecha": "2026-09-01",
	})
	var ed struct {
		Cantidad  int             `json:"cantidad"`
		PorRubro  map[string]int  `json:"por_rubro"`
		Avisos    []boletin.Aviso `json:"avisos"`
		Recortado string          `json:"recortado"`
	}
	decodificar(t, r, &ed)
	if ed.Cantidad != 100 {
		t.Errorf("cantidad = %d", ed.Cantidad)
	}
	// 100 avisos con el límite por defecto de 40: tiene que recortar y decirlo.
	if len(ed.Avisos) != 40 {
		t.Errorf("avisos = %d, se esperaban 40 por el límite", len(ed.Avisos))
	}
	if ed.Recortado == "" {
		t.Error("recortó la lista y no lo dijo")
	}
	if len(ed.PorRubro) == 0 {
		t.Error("faltan los rubros")
	}
}

func TestHerramientaEdicionConLimite(t *testing.T) {
	s := servidorDePrueba(t, false)
	r := llamarHerramienta(t, s, "edicion", map[string]any{
		"seccion": "primera", "fecha": "2026-09-01", "limite": 5,
	})
	var ed struct {
		Avisos []boletin.Aviso `json:"avisos"`
	}
	decodificar(t, r, &ed)
	if len(ed.Avisos) != 5 {
		t.Errorf("avisos = %d", len(ed.Avisos))
	}
}

func TestHerramientaEdicionFiltraPorRubro(t *testing.T) {
	s := servidorDePrueba(t, false)
	r := llamarHerramienta(t, s, "edicion", map[string]any{
		"seccion": "primera", "fecha": "2026-09-01", "rubro": "DECRETOS",
	})
	var ed struct {
		Cantidad int             `json:"cantidad"`
		Avisos   []boletin.Aviso `json:"avisos"`
	}
	decodificar(t, r, &ed)
	if ed.Cantidad == 0 || ed.Cantidad == 100 {
		t.Errorf("cantidad = %d", ed.Cantidad)
	}
	for _, a := range ed.Avisos {
		if a.Rubro != "DECRETOS" {
			t.Errorf("se coló %q", a.Rubro)
		}
	}
}

// Un día sin edición se explica en palabras, no como error: el modelo tiene
// que entender que es un feriado, no una falla.
func TestHerramientaEdicionSinEdicion(t *testing.T) {
	s := servidorDePrueba(t, false)
	r := llamarHerramienta(t, s, "edicion", map[string]any{
		"seccion": "primera", "fecha": "2026-08-17",
	})
	if r.EsError {
		t.Error("un día sin edición no es un error")
	}
	txt := strings.ToLower(r.Contenido[0].Texto)
	if !strings.Contains(txt, "no hubo edición") {
		t.Errorf("el mensaje no explica qué pasó: %s", r.Contenido[0].Texto)
	}
}

func TestHerramientaAviso(t *testing.T) {
	s := servidorDePrueba(t, false)
	r := llamarHerramienta(t, s, "aviso", map[string]any{
		"seccion": "primera", "id": "346633", "fecha": "2026-09-01",
	})
	var d struct {
		Organismo string          `json:"organismo"`
		Texto     string          `json:"texto"`
		Anexos    []boletin.Anexo `json:"anexos"`
		HTML      string          `json:"html"`
	}
	decodificar(t, r, &d)
	if d.Organismo != "PODER EJECUTIVO" {
		t.Errorf("organismo = %q", d.Organismo)
	}
	if len(d.Texto) < 200 {
		t.Errorf("texto de %d caracteres", len(d.Texto))
	}
	if len(d.Anexos) != 12 {
		t.Errorf("anexos = %d", len(d.Anexos))
	}
	// El HTML no le sirve al modelo y gasta contexto: no debería mandarse.
	if d.HTML != "" {
		t.Error("se mandó el HTML del aviso, que sólo gasta contexto")
	}
}

func TestHerramientaCalendarioYRubros(t *testing.T) {
	s := servidorDePrueba(t, false)

	var cal boletin.Calendario
	decodificar(t, llamarHerramienta(t, s, "calendario",
		map[string]any{"seccion": "primera", "anio": 2026}), &cal)
	if len(cal.Fechas) == 0 {
		t.Error("calendario vacío")
	}

	var rs []boletin.Rubro
	decodificar(t, llamarHerramienta(t, s, "rubros",
		map[string]any{"seccion": "primera"}), &rs)
	if len(rs) == 0 {
		t.Error("rubros vacíos")
	}
}

func TestHerramientaBuscar(t *testing.T) {
	s := servidorDePrueba(t, false)
	var b servicio.Busqueda
	decodificar(t, llamarHerramienta(t, s, "buscar", map[string]any{
		"seccion": "primera", "texto": "decreto",
		"desde": "2026-09-01", "hasta": "2026-09-03",
	}), &b)
	if b.Total == 0 {
		t.Error("la búsqueda no devolvió nada")
	}
	if b.Fuente != servicio.FuenteSitio {
		t.Errorf("sin índice, fuente = %q", b.Fuente)
	}
}

// Con índice, buscar sobre lo ya leído no le pide nada al Boletín.
func TestHerramientaBuscarUsaElIndice(t *testing.T) {
	s := servidorDePrueba(t, true)
	llamarHerramienta(t, s, "edicion", map[string]any{"seccion": "primera", "fecha": "2026-09-01"})

	var b servicio.Busqueda
	decodificar(t, llamarHerramienta(t, s, "buscar", map[string]any{
		"seccion": "primera", "texto": "economia",
		"desde": "2026-09-01", "hasta": "2026-09-01",
	}), &b)
	if b.Fuente != servicio.FuenteIndice {
		t.Errorf("fuente = %q, se esperaba indice", b.Fuente)
	}
	if b.Total == 0 {
		t.Error("no encontró nada en el índice")
	}
}

// Los errores de argumentos tienen que explicar cómo corregirlos.
func TestErroresDeArgumentosSonUtiles(t *testing.T) {
	s := servidorDePrueba(t, false)
	casos := []struct {
		nombre   string
		args     map[string]any
		contiene string
	}{
		{"edicion", map[string]any{}, "primera"},
		{"edicion", map[string]any{"seccion": "cuarta"}, "inválida"},
		{"aviso", map[string]any{"seccion": "primera", "fecha": "2026-09-01"}, "id"},
		{"aviso", map[string]any{"seccion": "primera", "id": "1", "fecha": "01/09/2026"}, "AAAA-MM-DD"},
		{"buscar", map[string]any{"seccion": "primera", "desde": "2026-09-03", "hasta": "2026-09-01"}, "revés"},
	}
	for _, c := range casos {
		t.Run(c.nombre+"/"+c.contiene, func(t *testing.T) {
			r := llamarHerramienta(t, s, c.nombre, c.args)
			if !r.EsError {
				t.Fatalf("se esperaba un error, vino: %s", r.Contenido[0].Texto)
			}
			if !strings.Contains(strings.ToLower(r.Contenido[0].Texto), strings.ToLower(c.contiene)) {
				t.Errorf("el mensaje no menciona %q: %s", c.contiene, r.Contenido[0].Texto)
			}
		})
	}
}

func TestHerramientaInexistente(t *testing.T) {
	s := servidorDePrueba(t, false)
	r := llamarHerramienta(t, s, "adivinar", map[string]any{})
	if !r.EsError {
		t.Error("pedir una herramienta que no existe tiene que ser un error")
	}
}

func TestEstado(t *testing.T) {
	s := servidorDePrueba(t, true)
	var e struct {
		IndiceLocal bool   `json:"indice_local"`
		Hoy         string `json:"hoy"`
	}
	decodificar(t, llamarHerramienta(t, s, "estado", map[string]any{}), &e)
	if !e.IndiceLocal {
		t.Error("indice_local = false con el motor sqlite")
	}
	if e.Hoy == "" {
		t.Error("no informó la fecha de hoy")
	}
}

// Por stdio, un mensaje por línea y una respuesta por línea.
func TestServirStdio(t *testing.T) {
	s := servidorDePrueba(t, false)
	entrada := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var salida strings.Builder
	if err := s.ServirStdio(context.Background(), entrada, &salida); err != nil {
		t.Fatal(err)
	}
	lineas := strings.Split(strings.TrimSpace(salida.String()), "\n")
	// Dos pedidos con id, una notificación sin respuesta.
	if len(lineas) != 2 {
		t.Fatalf("respuestas = %d, se esperaban 2:\n%s", len(lineas), salida.String())
	}
	for _, l := range lineas {
		var r Respuesta
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Errorf("respuesta no es JSON: %v", err)
		}
		if r.JSONRPC != "2.0" {
			t.Errorf("jsonrpc = %q", r.JSONRPC)
		}
	}
}
