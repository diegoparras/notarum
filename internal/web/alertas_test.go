package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/alertas"
	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/servicio"
)

// sitioConAlertas levanta el lector con una cuenta y las alertas encendidas.
func sitioConAlertas(t *testing.T) (*httptest.Server, *alertas.Registro) {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cuentas.NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CrearUsuario("diego", claveDePrueba, cuentas.RolAdmin); err != nil {
		t.Fatal(err)
	}
	srvcio := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm)
	sitio, err := Nuevo(srvcio, "test")
	if err != nil {
		t.Fatal(err)
	}
	regAlertas := alertas.NuevoRegistro(alm)
	sitio.ConCuentas(reg, cuentas.PoliticaPorDefecto(true)).
		ConAlertas(regAlertas, alertas.NuevoCorredor(regAlertas, srvcio, "https://x"))

	srv := httptest.NewServer(sitio)
	t.Cleanup(srv.Close)
	return srv, regAlertas
}

func entrarComoDiego(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	cli := navegador(t)
	res, _ := postear(t, cli, srv.URL+"/entrar", url.Values{
		"usuario": {"diego"}, "clave": {claveDePrueba},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("no entró: %d", res.StatusCode)
	}
	return cli
}

func TestCrearYBorrarUnaAlertaDesdeLaCuenta(t *testing.T) {
	srv, reg := sitioConAlertas(t)
	cli := entrarComoDiego(t, srv)

	_, cuerpo := postear(t, cli, srv.URL+"/cuenta/alertas", url.Values{
		"nombre": {"ENACOM"}, "fuente": {"nacional"}, "texto": {"enacom"},
	})
	if !strings.Contains(cuerpo, "ENACOM") {
		t.Error("la cuenta no muestra la alerta creada")
	}
	guardadas := reg.De("diego")
	if len(guardadas) != 1 {
		t.Fatalf("quedaron %d alertas", len(guardadas))
	}
	if guardadas[0].Fuente != alertas.FuenteNacional || guardadas[0].Criterios.Texto != "enacom" {
		t.Errorf("quedó como %+v", guardadas[0])
	}

	postear(t, cli, srv.URL+"/cuenta/alertas/"+guardadas[0].ID+"/borrar", url.Values{})
	if len(reg.De("diego")) != 0 {
		t.Error("no se borró")
	}
}

// Una alerta sin ningún criterio coincide con todo, y avisar de todo es lo
// mismo que no avisar.
func TestUnaAlertaSinCriteriosNoSeCrea(t *testing.T) {
	srv, reg := sitioConAlertas(t)
	cli := entrarComoDiego(t, srv)

	_, cuerpo := postear(t, cli, srv.URL+"/cuenta/alertas", url.Values{
		"nombre": {"todo"}, "fuente": {"nacional"},
	})
	if !strings.Contains(cuerpo, "coincide con todo") {
		t.Errorf("no explica por qué no se puede: %s", recorte(cuerpo))
	}
	if len(reg.De("diego")) != 0 {
		t.Error("se creó igual")
	}
}

// Un webhook a una dirección interna convierte a notarum en un mensajero de
// pedidos ajenos hacia adentro de su propia red.
func TestNoSeAceptaUnWebhookInterno(t *testing.T) {
	t.Setenv("NOTARUM_WEBHOOK_PERMITE_PRIVADAS", "")
	srv, reg := sitioConAlertas(t)
	cli := entrarComoDiego(t, srv)

	_, cuerpo := postear(t, cli, srv.URL+"/cuenta/alertas", url.Values{
		"nombre": {"interna"}, "fuente": {"nacional"}, "texto": {"x"},
		"webhook": {"http://169.254.169.254/latest/meta-data/"},
	})
	if !strings.Contains(cuerpo, "red interna") {
		t.Errorf("no explica por qué: %s", recorte(cuerpo))
	}
	if len(reg.De("diego")) != 0 {
		t.Error("se creó igual")
	}
}

// Los criterios que no son de la fuente elegida se descartan: guardarlos sin
// efecto promete un filtro que no existe.
func TestLosCriteriosAjenosALaFuenteSeDescartan(t *testing.T) {
	srv, reg := sitioConAlertas(t)
	cli := entrarComoDiego(t, srv)

	postear(t, cli, srv.URL+"/cuenta/alertas", url.Values{
		"nombre": {"mezcla"}, "fuente": {"boletin"}, "texto": {"algo"},
		"provincia": {"mendoza"}, "vigentes": {"1"},
	})
	a := reg.De("diego")
	if len(a) != 1 {
		t.Fatalf("quedaron %d", len(a))
	}
	if a[0].Criterios.Provincia != "" || a[0].Criterios.SoloVigentes {
		t.Errorf("se guardaron criterios que esa fuente no tiene: %+v", a[0].Criterios)
	}
}

// Nadie borra la alerta de otro por adivinar un identificador.
func TestNoSeBorraLaAlertaDeOtro(t *testing.T) {
	srv, reg := sitioConAlertas(t)
	ajena, err := reg.Crear(alertas.Alerta{
		Dueño: "otra", Nombre: "ajena", Fuente: alertas.FuenteNacional,
		Criterios: alertas.Criterios{Texto: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cli := entrarComoDiego(t, srv)
	postear(t, cli, srv.URL+"/cuenta/alertas/"+ajena.ID+"/borrar", url.Values{})
	if _, hay := reg.Leer(ajena.ID); !hay {
		t.Error("se borró la alerta de otra cuenta")
	}
}

func recorte(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
