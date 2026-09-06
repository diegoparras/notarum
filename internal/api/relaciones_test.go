package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/infoleg"
	"github.com/diegoparras/notarum/internal/servicio"
)

// El catálogo dice "modificada por 7" y no cuáles, que es un dato que no lleva
// a ningún lado. Estas rutas dan la lista, y salen de dos bases complementarias
// que se bajan con la misma sincronización.

const catalogoConRelaciones = `id_norma,tipo_norma,numero_norma,fecha_sancion,fecha_boletin,titulo_resumido,texto_original,modificada_por,modifica_a
24240,Ley,24240,1993-09-22,1993-10-15,DEFENSA DEL CONSUMIDOR,http://x/24240.htm,2,0
27250,Ley,27250,2016-06-08,2016-06-14,DEFENSA DEL CONSUMIDOR - MODIFICACION,,0,1
26361,Ley,26361,2008-03-12,2008-04-07,DEFENSA DEL CONSUMIDOR - MODIFICACION,,0,1
`

const modificadas = `id_norma_modificada,id_norma_modificatoria,tipo_norma,nro_norma,organismo_origen,fecha_boletin,titulo_resumido
24240,26361,Ley,26361,PODER LEGISLATIVO NACIONAL,2008-04-07,DEFENSA DEL CONSUMIDOR - MODIFICACION
24240,27250,Ley,27250,PODER LEGISLATIVO NACIONAL,2016-06-14,DEFENSA DEL CONSUMIDOR - MODIFICACION
`

const modificatorias = `id_norma_modificatoria,id_norma_modificada,tipo_norma,nro_norma,organismo_origen,fecha_boletin,titulo_resumido
27250,24240,Ley,24240,PODER LEGISLATIVO NACIONAL,1993-10-15,DEFENSA DEL CONSUMIDOR
`

func enZip(t *testing.T, nombre, contenido string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, err := z.Create(nombre)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(contenido)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// conRelaciones levanta la API con el catálogo y las dos complementarias ya
// sincronizadas, como quedan después de correr la actualización.
func conRelaciones(t *testing.T) *httptest.Server {
	t.Helper()
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/3/action/"):
			w.Write([]byte(`{"success":true,"result":{"metadata_modified":"2026-09-01T10:00:00.000000","resources":[
			  {"name":"Base Infoleg Normativa Nacional","format":"ZIP","url":"` + base + `/base.zip"},
			  {"name":"Base Complementaria Infoleg de Normas Modificadas","format":"ZIP","url":"` + base + `/mdas.zip"},
			  {"name":"Base Complementaria Infoleg de Normas Modificatorias","format":"ZIP","url":"` + base + `/mtorias.zip"}
			]}}`))
		case r.URL.Path == "/base.zip":
			w.Write(enZip(t, "base.csv", catalogoConRelaciones))
		case r.URL.Path == "/mdas.zip":
			w.Write(enZip(t, "mdas.csv", modificadas))
		case r.URL.Path == "/mtorias.zip":
			w.Write(enZip(t, "mtorias.csv", modificatorias))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(portal.Close)

	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConInfoLEG(infoleg.NuevoCliente(infoleg.Opciones{
			BaseDatos: portal.URL, Intervalo: time.Nanosecond,
		}))
	if _, err := srv.SincronizarInfoLEG(t.Context(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}

	api := httptest.NewServer(Nuevo(Config{Servicio: srv, Version: "test"}))
	t.Cleanup(api.Close)
	return api
}

func TestQueNormasModificaronAUna(t *testing.T) {
	srv := conRelaciones(t)
	res, cuerpo := pedir(t, srv, "/v1/nacional/24240/modificada-por")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("contestó %d: %s", res.StatusCode, cuerpo)
	}
	var r struct {
		ID            int `json:"id"`
		Total         int `json:"total"`
		ModificadaPor []struct {
			ID        int    `json:"id"`
			Tipo      string `json:"tipo"`
			Numero    string `json:"numero"`
			Organismo string `json:"organismo"`
			Ficha     string `json:"ficha"`
			EnNotarum string `json:"en_notarum"`
		} `json:"modificada_por"`
	}
	if err := json.Unmarshal(cuerpo, &r); err != nil {
		t.Fatalf("no es JSON: %v", err)
	}
	if r.Total != 2 || len(r.ModificadaPor) != 2 {
		t.Fatalf("la 24240 quedó con %d modificatorias: %s", r.Total, cuerpo)
	}
	// Con los datos de cada una al lado: sin eso habría que pedir dos normas
	// más para poder mostrar una lista.
	uno := r.ModificadaPor[0]
	if uno.Tipo != "Ley" || uno.Numero != "26361" || uno.Organismo == "" {
		t.Errorf("la primera quedó como %+v", uno)
	}
	if uno.EnNotarum != "/v1/nacional/26361" || !strings.Contains(uno.Ficha, "26361") {
		t.Errorf("los enlaces quedaron %q y %q", uno.EnNotarum, uno.Ficha)
	}
}

func TestQueNormasModificoUna(t *testing.T) {
	srv := conRelaciones(t)
	res, cuerpo := pedir(t, srv, "/v1/nacional/27250/modifica-a")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("contestó %d: %s", res.StatusCode, cuerpo)
	}
	var r struct {
		Total     int `json:"total"`
		ModificaA []struct {
			ID int `json:"id"`
		} `json:"modifica_a"`
	}
	json.Unmarshal(cuerpo, &r)
	if r.Total != 1 || r.ModificaA[0].ID != 24240 {
		t.Errorf("la 27250 modifica a %+v", r.ModificaA)
	}
	// Y no se confunden los sentidos: la 27250 modificó a la 24240, no al
	// revés. Leer el archivo con el sentido cambiado daría el índice dado
	// vuelta y nadie lo notaría hasta usarlo.
	_, alReves := pedir(t, srv, "/v1/nacional/24240/modifica-a")
	var m struct {
		Total int `json:"total"`
	}
	json.Unmarshal(alReves, &m)
	if m.Total != 0 {
		t.Errorf("la 24240 figura modificando a %d normas: los sentidos están cruzados", m.Total)
	}
}

// Una norma que no modificó a nadie y una que no existe llevan a cosas
// distintas: la primera es una lista vacía, la segunda hay que revisarla.
func TestUnaNormaSinRelacionesNoEsUnError(t *testing.T) {
	srv := conRelaciones(t)
	res, cuerpo := pedir(t, srv, "/v1/nacional/26361/modifica-a")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("contestó %d", res.StatusCode)
	}
	if !strings.Contains(string(cuerpo), `"total":0`) {
		t.Errorf("cuerpo = %s", cuerpo)
	}

	res, _ = pedir(t, srv, "/v1/nacional/999999/modificada-por")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("una norma que no existe contestó %d", res.StatusCode)
	}
}
