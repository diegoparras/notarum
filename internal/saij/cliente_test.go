package saij

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// respuestaPackageShow es la del portal real, recortada a lo que se usa.
const respuestaPackageShow = `{"help":"...","success":true,"result":{
  "title":"Base SAIJ de Normativa Provincial",
  "metadata_modified":"2026-09-01T13:57:50.433130",
  "resources":[
    {"name":"Base SAIJ de Normativa Provincial","format":"CSV",
     "url":"%s/download/base-saij-normativa-provincial.csv",
     "last_modified":"2026-09-01T10:57:16.703535","datastore_active":false},
    {"name":"Gráficos estadísticos","format":"gráfico",
     "url":"https://public.tableau.com/app/profile/justicia.abierta/viz/algo",
     "last_modified":null,"datastore_active":false}
  ]}}`

func portalFalso(t *testing.T, csv string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/3/action/package_show", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != Dataset {
			http.Error(w, "otro conjunto", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(strings.Replace(respuestaPackageShow, "%s", srv.URL, 1)))
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Write([]byte(csv))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestBuscarCatalogo(t *testing.T) {
	srv := portalFalso(t, "")
	c := NuevoCliente(Opciones{Base: srv.URL})

	info, err := c.BuscarCatalogo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// De los dos recursos hay que quedarse con el CSV y no con el tablero.
	if !strings.HasSuffix(info.URL, ".csv") {
		t.Errorf("URL = %q; se esperaba el CSV y no el otro recurso", info.URL)
	}
	esperado := time.Date(2026, 9, 1, 10, 57, 16, 703535000, time.UTC)
	if !info.Modificado.Equal(esperado) {
		t.Errorf("modificado = %v, se esperaba %v", info.Modificado, esperado)
	}
}

func TestDescargarCatalogo(t *testing.T) {
	contenido := "texto_actualizado,provincia_nombre,tipo_norma\nwww.saij.gob.ar/LPH0006109,Chaco,Ley\n"
	srv := portalFalso(t, contenido)
	c := NuevoCliente(Opciones{Base: srv.URL})

	info, err := c.BuscarCatalogo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	destino := filepath.Join(t.TempDir(), "catalogo.csv")
	if err := c.DescargarCatalogo(context.Background(), info.URL, destino); err != nil {
		t.Fatal(err)
	}
	crudo, err := os.ReadFile(destino)
	if err != nil {
		t.Fatal(err)
	}
	if string(crudo) != contenido {
		t.Errorf("se bajó otra cosa: %q", string(crudo))
	}
	// El archivo a medio bajar no puede quedar dando vueltas.
	if _, err := os.Stat(destino + ".parcial"); !os.IsNotExist(err) {
		t.Error("quedó el archivo parcial")
	}
}

// Una descarga cortada no puede dejar un catálogo a medias que parezca
// entero: la próxima sincronización lo leería como si estuviera completo.
func TestDescargaCortadaNoDejaCatalogoAMedias(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("texto_actualizado\nwww.saij.gob.ar/LPH0006109\n"))
		// Se corta sin mandar lo prometido.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		srvCortar(w)
	}))
	t.Cleanup(srv.Close)

	destino := filepath.Join(t.TempDir(), "catalogo.csv")
	err := NuevoCliente(Opciones{Base: srv.URL}).DescargarCatalogo(
		context.Background(), srv.URL+"/lo-que-sea", destino)
	if err == nil {
		t.Fatal("una descarga cortada se dio por buena")
	}
	if _, err := os.Stat(destino); !os.IsNotExist(err) {
		t.Error("quedó un catálogo a medias en el destino")
	}
}

// srvCortar corta la conexión sin terminar la respuesta.
func srvCortar(w http.ResponseWriter) {
	if h, ok := w.(http.Hijacker); ok {
		if con, _, err := h.Hijack(); err == nil {
			con.Close()
		}
	}
}

func TestPortalQueNoContesta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	_, err := NuevoCliente(Opciones{Base: srv.URL}).BuscarCatalogo(context.Background())
	var del *ErrDelPortal
	if !errors.As(err, &del) {
		t.Fatalf("err = %v; tendría que quedar claro que el problema es del portal", err)
	}
}

// Si el conjunto deja de publicar el CSV hay que decirlo, no seguir como si
// nada.
func TestConjuntoSinCSV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"result":{"resources":[
			{"name":"Gráficos","format":"gráfico","url":"https://public.tableau.com/x"}]}}`))
	}))
	t.Cleanup(srv.Close)

	_, err := NuevoCliente(Opciones{Base: srv.URL}).BuscarCatalogo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "CSV") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsearMomento(t *testing.T) {
	for entrada, esperado := range map[string]time.Time{
		"2026-09-01T10:57:16.703535": time.Date(2026, 9, 1, 10, 57, 16, 703535000, time.UTC),
		"2026-09-01T10:57:16":        time.Date(2026, 9, 1, 10, 57, 16, 0, time.UTC),
		"2026-09-01T10:57:16Z":       time.Date(2026, 9, 1, 10, 57, 16, 0, time.UTC),
	} {
		got, err := parsearMomento(entrada)
		if err != nil || !got.Equal(esperado) {
			t.Errorf("%q -> %v, %v", entrada, got, err)
		}
	}
	for _, malo := range []string{"", "  ", "ayer", "null"} {
		if _, err := parsearMomento(malo); err == nil {
			t.Errorf("se aceptó %q", malo)
		}
	}
}

// El portal pide identificarse con algo: mandar un User-Agent es lo mínimo
// para que del otro lado sepan quién está pidiendo.
func TestSeMandaElUserAgent(t *testing.T) {
	var visto string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visto = r.Header.Get("User-Agent")
		w.Write([]byte(`{"success":true,"result":{"resources":[]}}`))
	}))
	t.Cleanup(srv.Close)

	c := NuevoCliente(Opciones{Base: srv.URL, UserAgent: "notarum/1.2 (+https://ejemplo.ar)"})
	c.BuscarCatalogo(context.Background())
	if visto != "notarum/1.2 (+https://ejemplo.ar)" {
		t.Errorf("User-Agent = %q", visto)
	}
}
