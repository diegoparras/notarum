package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/infoleg"
	"github.com/diegoparras/notarum/internal/saij"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/tareas"
)

// sitioConPanel arma un lector con panel y dos cuentas: una que administra y
// otra que no.
func sitioConPanel(t *testing.T) (*httptest.Server, *tareas.Ejecutor) {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cuentas.NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CrearUsuario("jefa", claveDePrueba, cuentas.RolAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CrearUsuario("persona", claveDePrueba, cuentas.RolPersona); err != nil {
		t.Fatal(err)
	}
	srvcio := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConInfoLEG(infoleg.NuevoCliente(infoleg.Opciones{})).
		ConSAIJ(saij.NuevoCliente(saij.Opciones{}))
	sitio, err := Nuevo(srvcio, "test")
	if err != nil {
		t.Fatal(err)
	}
	ej := tareas.Nuevo()
	sitio.ConCuentas(reg, cuentas.PoliticaPorDefecto(true)).ConTareas(ej)

	srv := httptest.NewServer(sitio)
	t.Cleanup(srv.Close)
	return srv, ej
}

func entrarComo(t *testing.T, srv *httptest.Server, quien string) *http.Client {
	t.Helper()
	cli := navegador(t)
	res, _ := postear(t, cli, srv.URL+"/entrar", url.Values{
		"usuario": {quien}, "clave": {claveDePrueba},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("no entró %s: %d", quien, res.StatusCode)
	}
	return cli
}

func TestElPanelEsDelAdministrador(t *testing.T) {
	srv, _ := sitioConPanel(t)

	// Sin sesión, a entrar.
	res := pedirSinSeguir(t, navegador(t), srv.URL+"/admin")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/entrar" {
		t.Errorf("sin sesión = %d hacia %q", res.StatusCode, res.Header.Get("Location"))
	}

	// Con sesión pero sin ser admin, rebota.
	cli := entrarComo(t, srv, "persona")
	r2, cuerpo := pedirCon(t, cli, srv.URL+"/admin")
	if r2.StatusCode != http.StatusForbidden {
		t.Errorf("como persona = %d, se esperaba 403", r2.StatusCode)
	}
	if !strings.Contains(cuerpo, "administrador") {
		t.Error("no explica por qué no puede entrar")
	}

	// Y quien administra sí.
	jefa := entrarComo(t, srv, "jefa")
	r3, cuerpo3 := pedirCon(t, jefa, srv.URL+"/admin")
	if r3.StatusCode != http.StatusOK {
		t.Fatalf("como admin = %d", r3.StatusCode)
	}
	for _, que := range []string{"Panel", "InfoLEG", "Normativa provincial", "Boletín"} {
		if !strings.Contains(cuerpo3, que) {
			t.Errorf("falta %q en el panel", que)
		}
	}
}

// El enlace al panel aparece sólo para quien puede entrar. Que no aparezca es
// una cortesía; la protección es el gate.
func TestElEnlaceAlPanelSoloParaAdmins(t *testing.T) {
	srv, _ := sitioConPanel(t)

	_, dePersona := pedirCon(t, entrarComo(t, srv, "persona"), srv.URL+"/cuenta")
	if strings.Contains(dePersona, `href="/admin"`) {
		t.Error("a una cuenta común le aparece el enlace al panel")
	}
	_, deJefa := pedirCon(t, entrarComo(t, srv, "jefa"), srv.URL+"/cuenta")
	if !strings.Contains(deJefa, `href="/admin"`) {
		t.Error("a quien administra no le aparece el enlace al panel")
	}
}

// Lanzar una tarea desde el panel la pone a correr y devuelve a la página.
func TestLanzarUnaTareaDesdeElPanel(t *testing.T) {
	srv, ej := sitioConPanel(t)
	jefa := entrarComo(t, srv, "jefa")

	res, _ := postear(t, jefa, srv.URL+"/admin/tareas/provincial", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	// Termina en el panel, con un GET: recargar no puede relanzarla.
	if res.Request.URL.Path != "/admin" || res.Request.Method != http.MethodGet {
		t.Errorf("terminó en %s %s", res.Request.Method, res.Request.URL.Path)
	}
	// La tarea existió: o corre, o ya falló porque esta instancia no tiene
	// configurada la base provincial. Las dos cosas prueban que se lanzó.
	if e := ej.Estado("provincial").Estado; e == tareas.Nunca {
		t.Error("no se lanzó ninguna tarea")
	}
}

// Una tarea que no existe no se lanza.
func TestTareaInventada(t *testing.T) {
	srv, _ := sitioConPanel(t)
	jefa := entrarComo(t, srv, "jefa")
	res, _ := postear(t, jefa, srv.URL+"/admin/tareas/loquesea", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("codigo = %d", res.StatusCode)
	}
}

// Y una cuenta común no puede lanzar nada, ni yendo directo al formulario.
func TestUnaCuentaComunNoLanzaTareas(t *testing.T) {
	srv, ej := sitioConPanel(t)
	cli := entrarComo(t, srv, "persona")

	for _, tipo := range []string{"infoleg", "provincial", "rellenar"} {
		res, _ := postear(t, cli, srv.URL+"/admin/tareas/"+tipo, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s = %d, se esperaba 403", tipo, res.StatusCode)
		}
		if ej.Estado(tipo).Estado != tareas.Nunca {
			t.Errorf("se lanzó %s desde una cuenta común", tipo)
		}
	}
}

// Un rango mal escrito se avisa en la pantalla, no queda como una tarea
// fallada: el error es de quien completó el formulario.
func TestRellenarConUnRangoMalEscrito(t *testing.T) {
	srv, ej := sitioConPanel(t)
	jefa := entrarComo(t, srv, "jefa")

	casos := []url.Values{
		{"seccion": {"cuarta"}, "desde": {"2026-01-01"}, "hasta": {"2026-01-31"}},
		{"seccion": {"primera"}, "desde": {"ayer"}, "hasta": {"2026-01-31"}},
		{"seccion": {"primera"}, "desde": {"2026-03-01"}, "hasta": {"2026-01-31"}},
	}
	for _, c := range casos {
		res, cuerpo := postear(t, jefa, srv.URL+"/admin/tareas/rellenar", c)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%v -> %d", c, res.StatusCode)
		}
		if !strings.Contains(cuerpo, "aviso-error") {
			t.Errorf("%v: no se muestra el error en la pantalla", c)
		}
		if ej.Estado("rellenar").Estado != tareas.Nunca {
			t.Errorf("%v: se lanzó igual", c)
		}
	}
}

// El panel muestra cómo va una tarea que está corriendo, y con qué cortarla.
func TestElPanelMuestraLoQueCorre(t *testing.T) {
	srv, ej := sitioConPanel(t)
	jefa := entrarComo(t, srv, "jefa")

	seguir := make(chan struct{})
	defer close(seguir)
	ej.Lanzar("provincial", "jefa", func(_ context.Context, avisar func(string)) (string, error) {
		avisar("bajando el catálogo provincial")
		<-seguir
		return "", nil
	})
	for ej.Estado("provincial").Estado != tareas.Corriendo {
		time.Sleep(time.Millisecond)
	}

	_, cuerpo := pedirCon(t, jefa, srv.URL+"/admin")
	if !strings.Contains(cuerpo, "bajando el catálogo provincial") {
		t.Error("no se ve el avance de la tarea")
	}
	if !strings.Contains(cuerpo, "/admin/tareas/provincial/cortar") {
		t.Error("no hay con qué cortarla")
	}
	// Y la página se refresca sola mientras algo corre.
	if !strings.Contains(cuerpo, "http-equiv=\"refresh\"") {
		t.Error("la página no se actualiza sola")
	}
	// Con algo corriendo no se ofrece volver a lanzarlo.
	if strings.Contains(cuerpo, `action="/admin/tareas/provincial"`) {
		t.Error("ofrece lanzar una tarea que ya está corriendo")
	}
}

// Cortar desde el panel detiene la tarea.
func TestCortarDesdeElPanel(t *testing.T) {
	srv, ej := sitioConPanel(t)
	jefa := entrarComo(t, srv, "jefa")

	corriendo := make(chan struct{})
	ej.Lanzar("infoleg", "jefa", func(ctx context.Context, _ func(string)) (string, error) {
		close(corriendo)
		<-ctx.Done()
		return "lo que alcanzó", ctx.Err()
	})
	<-corriendo

	res, _ := postear(t, jefa, srv.URL+"/admin/tareas/infoleg/cortar", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	hasta := time.Now().Add(3 * time.Second)
	for time.Now().Before(hasta) {
		if ej.Estado("infoleg").Estado == tareas.Cortada {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("la tarea quedó en %q", ej.Estado("infoleg").Estado)
}

// El panel cuenta lo que la instancia tiene guardado, que es lo primero que
// mira quien la acaba de montar.
func TestElPanelCuentaQueHay(t *testing.T) {
	srv, _ := sitioConPanel(t)
	_, cuerpo := pedirCon(t, entrarComo(t, srv, "jefa"), srv.URL+"/admin")

	if !strings.Contains(cuerpo, "almacén") {
		t.Error("no dice qué almacén usa")
	}
	// Sin catálogos bajados, ofrece bajarlos.
	if !strings.Contains(cuerpo, "Bajar la normativa provincial") {
		t.Error("no ofrece bajar la normativa provincial")
	}
	if !strings.Contains(cuerpo, "Sincronizar InfoLEG") {
		t.Error("no ofrece sincronizar InfoLEG")
	}
}

func TestConPuntos(t *testing.T) {
	for entrada, esperado := range map[int]string{
		0: "0", 42: "42", 999: "999", 1000: "1.000",
		81403: "81.403", 428000: "428.000", 1234567: "1.234.567",
	} {
		if got := conPuntos(entrada); got != esperado {
			t.Errorf("%d -> %q, se esperaba %q", entrada, got, esperado)
		}
	}
}

func TestHaceCuanto(t *testing.T) {
	ahora := time.Now()
	casos := []struct {
		cuando   time.Time
		contiene string
	}{
		{ahora.Add(-10 * time.Second), "recién"},
		{ahora.Add(-5 * time.Minute), "minutos"},
		{ahora.Add(-3 * time.Hour), "horas"},
		{ahora.Add(-30 * time.Hour), "ayer"},
		{ahora.Add(-72 * time.Hour), "días"},
	}
	for _, c := range casos {
		if got := haceCuanto(c.cuando); !strings.Contains(got, c.contiene) {
			t.Errorf("%v -> %q, se esperaba algo con %q", c.cuando, got, c.contiene)
		}
	}
	if got := haceCuanto(time.Time{}); got != "" {
		t.Errorf("sin fecha = %q", got)
	}
}

// Cambiar la política desde el panel rige en el acto: es lo que evita tener
// que tocar variables de entorno y volver a desplegar.
func TestCambiarLaPoliticaDesdeElPanel(t *testing.T) {
	srv, _ := sitioConPanel(t)
	jefa := entrarComo(t, srv, "jefa")

	res, cuerpo := postear(t, jefa, srv.URL+"/admin/politica", url.Values{
		"modo": {"mixto"}, "anonimo": {"7"}, "persona": {"70"},
		"admin": {"700"}, "lector": {"77"}, "login": {"3"},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	// Vuelve al panel con lo nuevo puesto.
	if !strings.Contains(cuerpo, `value="7"`) || !strings.Contains(cuerpo, `value="mixto" selected`) {
		t.Error("el panel no muestra lo que se acaba de guardar")
	}
	if !strings.Contains(cuerpo, "se configuró desde acá") {
		t.Error("no aclara que lo que rige salió del panel")
	}

	// Y rige de verdad: la cuenta muestra la cuota nueva.
	_, cuenta := pedirCon(t, jefa, srv.URL+"/cuenta")
	if !strings.Contains(cuenta, "700") {
		t.Error("la cuota nueva no llegó a la cuenta")
	}
}

// Sobrevive al reinicio: se guarda, no queda sólo en memoria.
func TestLaPoliticaSobreviveAlReinicio(t *testing.T) {
	dir := t.TempDir()
	alm, err := almacen.NuevoDisco(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cuentas.NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	p := cuentas.PoliticaPorDefecto(true)
	reg.CargarPolitica(p)
	p.Modo = cuentas.ModoAbierto
	p.Anonimo = 123
	if err := reg.FijarPolitica(p); err != nil {
		t.Fatal(err)
	}

	// Otro registro sobre el mismo almacén: es lo que pasa al reiniciar.
	alm2, _ := almacen.NuevoDisco(dir)
	reg2, err := cuentas.NuevoRegistro(alm2, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	reg2.CargarPolitica(cuentas.PoliticaPorDefecto(true)) // el entorno dice otra cosa
	if got := reg2.Politica(); got.Modo != cuentas.ModoAbierto || got.Anonimo != 123 {
		t.Errorf("después de reiniciar rige %+v", got)
	}
}

// Un número imposible se avisa y no se guarda: una cuota en cero deja a todos
// afuera sin que nadie entienda por qué.
func TestUnaCuotaImposibleNoSeGuarda(t *testing.T) {
	srv, _ := sitioConPanel(t)
	jefa := entrarComo(t, srv, "jefa")
	antes := url.Values{"modo": {"abierto"}, "anonimo": {"60"}, "persona": {"600"},
		"admin": {"6000"}, "lector": {"600"}, "login": {"10"}}
	postear(t, jefa, srv.URL+"/admin/politica", antes)

	for _, malo := range []url.Values{
		{"modo": {"abierto"}, "anonimo": {"0"}},
		{"modo": {"abierto"}, "anonimo": {"-5"}},
		{"modo": {"abierto"}, "anonimo": {"ninguno"}},
		{"modo": {"publico"}, "anonimo": {"60"}},
	} {
		res, _ := postear(t, jefa, srv.URL+"/admin/politica", malo)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%v -> %d, se esperaba 400", malo, res.StatusCode)
		}
	}
	// Y lo que regía quedó intacto.
	_, cuerpo := pedirCon(t, jefa, srv.URL+"/admin")
	if !strings.Contains(cuerpo, `value="60"`) {
		t.Error("se perdió la configuración que andaba")
	}
}

// Y se puede volver a lo que diga el entorno.
func TestVolverALaConfiguracionDelServicio(t *testing.T) {
	srv, _ := sitioConPanel(t)
	jefa := entrarComo(t, srv, "jefa")
	postear(t, jefa, srv.URL+"/admin/politica", url.Values{
		"modo": {"abierto"}, "anonimo": {"7"},
	})
	res, cuerpo := postear(t, jefa, srv.URL+"/admin/politica/olvidar", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if strings.Contains(cuerpo, "se configuró desde acá") {
		t.Error("sigue diciendo que la política salió del panel")
	}
	if strings.Contains(cuerpo, `value="7"`) {
		t.Error("no volvió a la configuración del servicio")
	}
}

// Una cuenta común no puede cambiar quién entra.
func TestSoloElAdminCambiaLaPolitica(t *testing.T) {
	srv, _ := sitioConPanel(t)
	cli := entrarComo(t, srv, "persona")
	for _, ruta := range []string{"/admin/politica", "/admin/politica/olvidar"} {
		res, _ := postear(t, cli, srv.URL+ruta, url.Values{"modo": {"abierto"}})
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s = %d, se esperaba 403", ruta, res.StatusCode)
		}
	}
}
