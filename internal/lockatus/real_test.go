package lockatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Estos dos archivos salieron de un Lockatus de verdad, levantado para esto.
// Un cliente escrito contra la documentación puede estar equivocado en los
// detalles que la documentación no menciona; contra la respuesta del hub, no.

func leerFixture(t *testing.T, nombre string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", nombre))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// El JWKS del hub tiene que entrar en la estructura con la que se verifican
// las firmas, y salir de ahí como una clave RSA usable.
func TestElJWKSDeUnHubRealSeEntiende(t *testing.T) {
	var doc struct {
		Claves []clavePublica `json:"keys"`
	}
	if err := json.Unmarshal(leerFixture(t, "jwks_lockatus.json"), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Claves) == 0 {
		t.Fatal("el hub no publicó ninguna clave")
	}
	for _, k := range doc.Claves {
		if k.Kid == "" {
			t.Error("la clave no trae kid; sin él no se puede elegir cuál usar")
		}
		pub, err := k.rsa()
		if err != nil {
			t.Fatalf("la clave del hub no se pudo armar: %v", err)
		}
		if pub.Size()*8 < 2048 {
			t.Errorf("la clave del hub es de %d bits", pub.Size()*8)
		}
		if pub.E != 65537 {
			t.Errorf("exponente = %d; el de AQAB es 65537", pub.E)
		}
	}
}

// Las direcciones que arma el cliente tienen que ser las que el hub declara.
// Este es el error que la documentación no evita: escribir /token cuando el
// hub lo llama de otra manera.
func TestLasDireccionesCoincidenConLasDelHub(t *testing.T) {
	var d struct {
		Emisor        string   `json:"issuer"`
		Autorizacion  string   `json:"authorization_endpoint"`
		Token         string   `json:"token_endpoint"`
		JWKS          string   `json:"jwks_uri"`
		Metodos       []string `json:"code_challenge_methods_supported"`
		Algoritmos    []string `json:"id_token_signing_alg_values_supported"`
		TiposDeCodigo []string `json:"response_types_supported"`
	}
	if err := json.Unmarshal(leerFixture(t, "discovery_lockatus.json"), &d); err != nil {
		t.Fatal(err)
	}

	c, err := Nuevo(Opciones{Emisor: d.Emisor, ClienteID: "notarum", Vuelta: "http://x/v"})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := NuevaTransaccion("")
	if err != nil {
		t.Fatal(err)
	}
	if antes, _, _ := strings.Cut(c.URLAutorizar(tr), "?"); antes != d.Autorizacion {
		t.Errorf("autorización: se usa %q y el hub declara %q", antes, d.Autorizacion)
	}
	// Las otras dos se arman adentro; se comprueban por la misma regla.
	if c.emisor+"/token" != d.Token {
		t.Errorf("token: se usa %q y el hub declara %q", c.emisor+"/token", d.Token)
	}
	if c.emisor+"/jwks.json" != d.JWKS {
		t.Errorf("jwks: se usa %q y el hub declara %q", c.emisor+"/jwks.json", d.JWKS)
	}

	// Y lo que el cliente exige tiene que ser algo que el hub sepa hacer.
	if !tiene(d.Metodos, "S256") {
		t.Errorf("el hub no soporta S256, que es lo único que se manda: %v", d.Metodos)
	}
	if !tiene(d.Algoritmos, "RS256") {
		t.Errorf("el hub no firma con RS256, que es lo único que se acepta: %v", d.Algoritmos)
	}
	if !tiene(d.TiposDeCodigo, "code") {
		t.Errorf("el hub no hace el flujo de código: %v", d.TiposDeCodigo)
	}
}

func tiene(lista []string, que string) bool {
	for _, v := range lista {
		if v == que {
			return true
		}
	}
	return false
}
