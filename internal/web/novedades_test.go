package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/diegoparras/notarum"
	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/novedades"
	"github.com/diegoparras/notarum/internal/servicio"
)

// versionActual es la primera del CHANGELOG: la que el test de cmd/notarum
// exige que coincida con la del código.
func versionActual(t *testing.T) string {
	t.Helper()
	vs := novedades.Parsear(notarum.Changelog)
	if len(vs) == 0 {
		t.Fatal("el CHANGELOG no tiene ninguna versión")
	}
	return vs[0].Version
}

// sitioConVersion arma un lector que dice correr la versión v. No sale a la
// red: las páginas que se piden acá no leen el Boletín.
func sitioConVersion(t *testing.T, v string) *httptest.Server {
	t.Helper()
	cli := boletin.NuevoCliente(boletin.Opciones{Base: "http://127.0.0.1:1"})
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sitio, err := Nuevo(servicio.Nuevo(cli, alm), v)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(sitio)
	t.Cleanup(srv.Close)
	return srv
}

func pedirConCookie(t *testing.T, srv *httptest.Server, ruta, vista string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+ruta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if vista != "" {
		req.AddCookie(&http.Cookie{Name: cookieVersionVista, Value: vista})
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", ruta, err)
	}
	defer res.Body.Close()
	cuerpo, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("leyendo %s: %v", ruta, err)
	}
	return res, string(cuerpo)
}

func cookieVista(res *http.Response) string {
	for _, c := range res.Cookies() {
		if c.Name == cookieVersionVista {
			return c.Value
		}
	}
	return ""
}

// TestPaginaDeNovedades: /novedades muestra lo que cambió en cada versión,
// hasta la que corre, y con ?desde= sólo lo posterior a esa.
func TestPaginaDeNovedades(t *testing.T) {
	v := versionActual(t)
	srv := sitioConVersion(t, v)

	res, html := pedirConCookie(t, srv, "/novedades", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/novedades respondió %d", res.StatusCode)
	}
	if !strings.Contains(html, "v"+v) {
		t.Errorf("/novedades no muestra la versión %s", v)
	}
	if !strings.Contains(html, "<strong>Cada deploy dice qué versión es y qué cambia</strong>") &&
		!strings.Contains(html, "nov-list") {
		t.Errorf("/novedades no muestra las notas: %.400s", html)
	}

	_, html = pedirConCookie(t, srv, "/novedades?desde="+v, "")
	if strings.Contains(html, `class="nov-ver"`) {
		t.Error("/novedades?desde=<la actual> no tendría que listar ninguna versión")
	}
	_, html = pedirConCookie(t, srv, "/novedades?desde=<script>", "")
	if strings.Contains(html, "<script>") {
		t.Error("un ?desde= que no es versión no se puede devolver crudo")
	}
}

// TestNotasEscapadas: el markdown de las notas se convierte DESPUÉS de
// escapar el HTML.
func TestNotasEscapadas(t *testing.T) {
	got := string(notaHTML("**ojo** con `<b>` y <script>alert(1)</script>"))
	want := "<strong>ojo</strong> con <code>&lt;b&gt;</code> y &lt;script&gt;alert(1)&lt;/script&gt;"
	if got != want {
		t.Errorf("notaHTML = %q, se esperaba %q", got, want)
	}
}

// TestAvisoDeActualizacion: quien vuelve después de una actualización ve una
// vez qué cambió; quien entra por primera vez o ya la vio, no ve nada. La
// cookie queda siempre en la versión que corre.
func TestAvisoDeActualizacion(t *testing.T) {
	v := versionActual(t)
	srv := sitioConVersion(t, v)

	res, html := pedirConCookie(t, srv, "/docs", "0.0.1")
	if !strings.Contains(html, `class="contenedor aviso-version"`) {
		t.Fatalf("con una versión vieja vista no aparece el aviso: %.600s", html)
	}
	if !strings.Contains(html, "/novedades?desde=0.0.1") {
		t.Error("el aviso no enlaza a lo que cambió desde la versión vista")
	}
	if got := cookieVista(res); got != v {
		t.Errorf("cookie = %q, se esperaba %q", got, v)
	}

	res, html = pedirConCookie(t, srv, "/docs", v)
	if strings.Contains(html, `class="contenedor aviso-version"`) {
		t.Error("ya vista: no tendría que aparecer el aviso")
	}
	if got := cookieVista(res); got != "" {
		t.Errorf("ya vista: no hace falta volver a mandar la cookie, llegó %q", got)
	}

	res, html = pedirConCookie(t, srv, "/docs", "")
	if strings.Contains(html, `class="contenedor aviso-version"`) {
		t.Error("primera vez: no tendría que aparecer el aviso")
	}
	if got := cookieVista(res); got != v {
		t.Errorf("primera vez: cookie = %q, se esperaba %q", got, v)
	}

	_, html = pedirConCookie(t, srv, "/docs", "9.9.9")
	if strings.Contains(html, `class="contenedor aviso-version"`) {
		t.Error("una versión vista POSTERIOR a la que corre (vuelta atrás) no es una actualización")
	}
}

// TestMenuYPieEnlazanNovedades: el menú tiene "novedades" y el pie, al lado
// de la versión, "qué cambió".
func TestMenuYPieEnlazanNovedades(t *testing.T) {
	srv := sitioConVersion(t, versionActual(t))
	_, html := pedirConCookie(t, srv, "/docs", "")
	if !strings.Contains(html, `class="menu-item" href="/novedades"`) {
		t.Error("el menú no tiene novedades")
	}
	if !strings.Contains(html, `href="/novedades">qué cambió</a>`) {
		t.Error("el pie no enlaza qué cambió")
	}
}
