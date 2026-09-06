package armador

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/contrato"
)

func rutaDeEjemplo() contrato.Ruta {
	return contrato.Ruta{
		Metodo:  "GET",
		Camino:  "/v1/ediciones/{seccion}",
		Resumen: "Edición de un día",
		Parametros: []contrato.Parametro{
			{Nombre: "seccion", En: "path", Obligatorio: true, Opciones: []string{"primera", "segunda"}},
			{Nombre: "desde", En: "query", Ejemplo: "2026-01-01"},
			{Nombre: "hasta", En: "query", Ejemplo: "2026-01-31"},
			{Nombre: "limite", En: "query"}, // sin ejemplo: no se pone
		},
	}
}

func TestLaURLPoneLosParametrosDondeVan(t *testing.T) {
	got := URL("https://notarum.example/", rutaDeEjemplo(), nil)
	const quiero = "https://notarum.example/v1/ediciones/primera?desde=2026-01-01&hasta=2026-01-31"
	if got != quiero {
		t.Errorf("URL = %q", got)
	}
}

func TestLoQueSePideLePisaAlEjemplo(t *testing.T) {
	got := URL("https://x", rutaDeEjemplo(), Valores{"seccion": "segunda", "desde": "2020-05-05"})
	if !strings.Contains(got, "/v1/ediciones/segunda") || !strings.Contains(got, "desde=2020-05-05") {
		t.Errorf("URL = %q", got)
	}
}

// El & sin comillas hace que el shell tome la mitad de la dirección como otra
// cosa: la URL va entrecomillada siempre.
func TestElCurlLlevaLaURLEntreComillas(t *testing.T) {
	got := Curl("https://x", rutaDeEjemplo(), nil, true)
	if !strings.Contains(got, `"https://x/v1/ediciones/primera?desde=`) {
		t.Errorf("curl = %q", got)
	}
	if !strings.Contains(got, "Authorization: Bearer "+TokenDeEjemplo) {
		t.Errorf("no lleva el token: %q", got)
	}
}

// Lo que importa de n8n: que sea un nodo que n8n acepte. Un JSON válido que no
// tenga la forma que espera se pega y no aparece nada.
func TestElNodoDeN8NTieneLaFormaQueN8NEspera(t *testing.T) {
	crudo, err := N8N("https://notarum.example", rutaDeEjemplo(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var pegable struct {
		Nodos []struct {
			Parametros struct {
				URL        string `json:"url"`
				MandaQuery bool   `json:"sendQuery"`
				Query      struct {
					Parametros []struct {
						Nombre string `json:"name"`
						Valor  string `json:"value"`
					} `json:"parameters"`
				} `json:"queryParameters"`
				MandaHeaders bool `json:"sendHeaders"`
				Headers      struct {
					Parametros []struct {
						Nombre string `json:"name"`
						Valor  string `json:"value"`
					} `json:"parameters"`
				} `json:"headerParameters"`
			} `json:"parameters"`
			Tipo    string  `json:"type"`
			Version float64 `json:"typeVersion"`
			ID      string  `json:"id"`
			Nombre  string  `json:"name"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(crudo), &pegable); err != nil {
		t.Fatalf("no es JSON válido: %v", err)
	}
	if len(pegable.Nodos) != 1 {
		t.Fatalf("trae %d nodos", len(pegable.Nodos))
	}
	n := pegable.Nodos[0]

	// Sin esto n8n no sabe qué nodo es y no lo pega.
	if n.Tipo != "n8n-nodes-base.httpRequest" {
		t.Errorf("type = %q", n.Tipo)
	}
	if n.Version < 4 {
		t.Errorf("typeVersion = %v", n.Version)
	}
	// El id tiene que ser un UUID: n8n los usa para identificar cada nodo.
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuid.MatchString(n.ID) {
		t.Errorf("id = %q y no parece un UUID", n.ID)
	}

	// La URL, sin los parámetros de consulta: en n8n van aparte, para poder
	// editarlos desde el nodo.
	if n.Parametros.URL != "https://notarum.example/v1/ediciones/primera" {
		t.Errorf("url = %q", n.Parametros.URL)
	}
	if strings.Contains(n.Parametros.URL, "?") {
		t.Error("la query quedó pegada a la URL en vez de ir en su lista")
	}
	if !n.Parametros.MandaQuery || len(n.Parametros.Query.Parametros) != 2 {
		t.Errorf("los parámetros de consulta quedaron %+v", n.Parametros.Query)
	}
	if !n.Parametros.MandaHeaders || len(n.Parametros.Headers.Parametros) != 1 {
		t.Errorf("el token no quedó en las cabeceras")
	}
	if n.Parametros.Headers.Parametros[0].Valor != "Bearer "+TokenDeEjemplo {
		t.Errorf("cabecera = %q", n.Parametros.Headers.Parametros[0].Valor)
	}
}

// Sin token no se manda una cabecera vacía, que en n8n queda como un campo a
// completar que nadie pidió.
func TestSinTokenNoHayCabeceras(t *testing.T) {
	crudo, _ := N8N("https://x", rutaDeEjemplo(), nil, false)
	if strings.Contains(crudo, "headerParameters") || strings.Contains(crudo, "sendHeaders") {
		t.Errorf("mandó cabeceras sin que hicieran falta:\n%s", crudo)
	}
}

// Armar dos veces la misma consulta tiene que dar exactamente lo mismo: si
// cambia, uno se pregunta qué cambió.
func TestArmarDosVecesDaLoMismo(t *testing.T) {
	a, _ := N8N("https://x", rutaDeEjemplo(), nil, true)
	b, _ := N8N("https://x", rutaDeEjemplo(), nil, true)
	if a != b {
		t.Error("armar lo mismo dos veces dio distinto")
	}
}

// Contra el contrato de verdad, que es de donde salen las rutas: todas tienen
// que poder armarse, no sólo la de ejemplo.
func TestTodasLasRutasDelContratoSePuedenArmar(t *testing.T) {
	doc, err := contrato.Leer()
	if err != nil {
		t.Fatal(err)
	}
	var cuantas int
	for _, g := range doc.Grupos {
		for _, r := range g.Rutas {
			cuantas++
			crudo, err := N8N("https://notarum.example", r, nil, true)
			if err != nil {
				t.Errorf("%s %s: %v", r.Metodo, r.Camino, err)
				continue
			}
			var comprobar map[string]any
			if err := json.Unmarshal([]byte(crudo), &comprobar); err != nil {
				t.Errorf("%s %s: no es JSON válido: %v", r.Metodo, r.Camino, err)
			}
			// Sin parámetros de camino sin reemplazar: una URL con {seccion}
			// adentro no sirve para nada.
			url := URL("https://notarum.example", r, nil)
			if strings.Contains(url, "{") {
				t.Errorf("%s %s quedó con un hueco sin llenar: %s", r.Metodo, r.Camino, url)
			}
		}
	}
	if cuantas == 0 {
		t.Fatal("el contrato no trajo ninguna ruta")
	}
	t.Logf("se armaron %d rutas", cuantas)
}
