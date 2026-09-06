package web

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/contrato"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/mcp"
)

// La documentación se dibuja del contrato y de la lista de herramientas: no
// puede quedar desactualizada, y estos tests lo verifican comparando la página
// contra esas mismas fuentes.
func TestDocsSeDibuja(t *testing.T) {
	srv := sitioDePrueba(t)
	res, html := pedir(t, srv, "/docs")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	for _, esperado := range []string{
		"Documentación", "API", "MCP", "Errores",
		"/v1/openapi.json",
		"If-None-Match", // la parte de las cabeceras
		"sin_edicion",   // el caso del feriado
	} {
		if !strings.Contains(html, esperado) {
			t.Errorf("la página no contiene %q", esperado)
		}
	}
}

// Toda ruta del contrato tiene que aparecer documentada. Si mañana se agrega
// una y no sale, este test lo dice.
func TestDocsMuestraTodasLasRutasDelContrato(t *testing.T) {
	doc, err := contrato.Leer()
	if err != nil {
		t.Fatal(err)
	}
	srv := sitioDePrueba(t)
	_, html := pedir(t, srv, "/docs")

	var rutas int
	for _, g := range doc.Grupos {
		for _, r := range g.Rutas {
			rutas++
			if !strings.Contains(html, r.Camino) {
				t.Errorf("falta la ruta %s en la página", r.Camino)
			}
			if r.Resumen != "" && !strings.Contains(html, r.Resumen) {
				t.Errorf("falta el resumen de %s", r.Camino)
			}
		}
	}
	if rutas == 0 {
		t.Fatal("el contrato no trajo ninguna ruta")
	}
}

// Y toda herramienta MCP también.
func TestDocsMuestraTodasLasHerramientas(t *testing.T) {
	srv := sitioDePrueba(t)
	_, html := pedir(t, srv, "/docs")

	hs := mcp.Herramientas()
	if len(hs) == 0 {
		t.Fatal("no hay herramientas declaradas")
	}
	for _, h := range hs {
		if !strings.Contains(html, h.Nombre) {
			t.Errorf("falta la herramienta %q", h.Nombre)
		}
		if !strings.Contains(html, h.Descripcion) {
			t.Errorf("falta la descripción de %q", h.Nombre)
		}
	}
}

// Los argumentos de una herramienta se muestran con su tipo y cuáles son
// obligatorios: es lo que hace falta para usarla.
func TestDocsMuestraLosArgumentos(t *testing.T) {
	srv := sitioDePrueba(t)
	_, html := pedir(t, srv, "/docs")

	for _, esperado := range []string{
		"Palabras a buscar. No hace falta poner los acentos.", // de buscar
		"obligatorio",
		"Cuántos avisos traer",
	} {
		if !strings.Contains(html, esperado) {
			t.Errorf("la página no contiene %q", esperado)
		}
	}
}

// Los ejemplos tienen que poder copiarse y funcionar: con la dirección real y
// sin huecos sin llenar.
func TestDocsEjemplosCopiables(t *testing.T) {
	srv := sitioDePrueba(t)
	_, html := pedir(t, srv, "/docs")

	if !strings.Contains(html, srv.URL+"/v1/ediciones/primera/2026-09-01") {
		t.Error("el ejemplo no trae la dirección de esta instancia")
	}
	if !strings.Contains(html, "curl -s") {
		t.Error("el ejemplo no es una línea de curl")
	}
	// La URL va entre comillas —escapadas por la plantilla— porque sin ellas
	// el shell corta la dirección en el &.
	if !strings.Contains(html, "&#34;"+srv.URL) {
		t.Error("la dirección no quedó entrecomillada")
	}
	// Y el nodo de n8n, que es lo que se pega en el lienzo.
	if !strings.Contains(html, "n8n-nodes-base.httpRequest") {
		t.Error("no está el nodo de n8n")
	}
	// Un hueco sin llenar en un ejemplo sería un copiar y pegar roto.
	i := strings.Index(html, "ruta-ejemplo")
	for i >= 0 {
		fin := strings.Index(html[i:], "</div>")
		if fin < 0 {
			break
		}
		bloque := html[i : i+fin]
		if strings.Contains(bloque, "{seccion}") || strings.Contains(bloque, "{fecha}") {
			t.Errorf("un ejemplo quedó con huecos: %s", bloque)
		}
		siguiente := strings.Index(html[i+fin:], "ruta-ejemplo")
		if siguiente < 0 {
			break
		}
		i = i + fin + siguiente
	}
}

// Los esquemas se muestran con sus campos, incluidos los que hereda el detalle
// del aviso.
func TestDocsMuestraLasFormas(t *testing.T) {
	srv := sitioDePrueba(t)
	_, html := pedir(t, srv, "/docs")

	for _, esperado := range []string{"Aviso", "Detalle", "Edicion", "tiene_anexos", "por_rubro"} {
		if !strings.Contains(html, esperado) {
			t.Errorf("falta %q en las formas", esperado)
		}
	}
}

// Con el endpoint HTTP apagado, las herramientas se siguen documentando —el
// binario las habla por entrada estándar igual— pero no se invita a llamar a
// una dirección que no responde.
func TestDocsConEndpointMCPApagado(t *testing.T) {
	srv := sitioDePrueba(t) // sin ConMCP: el endpoint está apagado
	res, html := pedir(t, srv, "/docs")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(html, "endpoint HTTP está apagado") {
		t.Error("no se avisó que el endpoint está apagado")
	}
	if strings.Contains(html, "curl -X POST") {
		t.Error("se invitó a llamar a un endpoint apagado")
	}
	// Pero las herramientas se documentan igual: existen por stdio.
	for _, h := range mcp.Herramientas() {
		if !strings.Contains(html, h.Nombre) {
			t.Errorf("falta la herramienta %q", h.Nombre)
		}
	}
	if !strings.Contains(html, "mcpServers") {
		t.Error("falta cómo conectarlo por entrada estándar")
	}
}

// La documentación se enlaza desde la cabecera y el pie: si no, nadie la
// encuentra.
func TestDocsSeEnlazaDesdeElSitio(t *testing.T) {
	srv := sitioDePrueba(t)
	_, html := pedir(t, srv, "/ed/primera/2026-09-01")
	if strings.Count(html, `href="/docs"`) < 2 {
		t.Error("la documentación tendría que enlazarse desde la cabecera y el pie")
	}
}

// La dirección de los ejemplos sale de lo que ve quien mira, incluso detrás de
// un proxy que termina el TLS.
func TestBaseVisible(t *testing.T) {
	casos := []struct {
		host, proto, hostReenviado, esperado string
	}{
		{"notarum.local:8080", "", "", "http://notarum.local:8080"},
		{"interno:8080", "https", "", "https://interno:8080"},
		{"interno:8080", "https", "boletin.midominio.com", "https://boletin.midominio.com"},
		{"interno", "HTTPS", "", "https://interno"},
	}
	for _, c := range casos {
		r := httptest.NewRequest("GET", "/docs", nil)
		r.Host = c.host
		if c.proto != "" {
			r.Header.Set("X-Forwarded-Proto", c.proto)
		}
		if c.hostReenviado != "" {
			r.Header.Set("X-Forwarded-Host", c.hostReenviado)
		}
		if got := baseVisible(r); got != c.esperado {
			t.Errorf("host=%q proto=%q -> %q, se esperaba %q", c.host, c.proto, got, c.esperado)
		}
	}
}

// La documentación tiene que decir en qué modo está la instancia y con qué
// cuota: no puede afirmar que todo es abierto, porque eso lo decide quien la
// levanta.
func TestDocsMuestraElAcceso(t *testing.T) {
	srv := sitioDePrueba(t)
	_, cuerpo := pedir(t, srv, "/docs")

	p := cuentas.PoliticaPorDefecto(false)
	if !strings.Contains(cuerpo, p.Explicacion()) {
		t.Errorf("la documentación no explica el modo: falta %q", p.Explicacion())
	}
	if !strings.Contains(cuerpo, strconv.Itoa(p.Anonimo)) {
		t.Error("no dice la cuota de quien no se identifica")
	}
	if !strings.Contains(cuerpo, "Authorization: Bearer") {
		t.Error("no dice cómo se manda el token")
	}
	// Y no puede quedar afirmando lo contrario.
	if strings.Contains(cuerpo, "sin clave") {
		t.Error("la documentación sigue diciendo que no hace falta clave")
	}
	if strings.Contains(cuerpo, "acceso </span>") {
		t.Error("el modo salió vacío")
	}
}
