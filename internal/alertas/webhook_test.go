package alertas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// La dirección la pone quien crea la alerta y notarum es el que sale a
// buscarla: sin control, alguien con una cuenta usa a notarum para llegar a
// cosas que sólo se alcanzan desde adentro de la red donde corre.
func TestNoSeAvisaADireccionesInternas(t *testing.T) {
	t.Setenv(permitirPrivadasVar, "")
	for _, malo := range []string{
		"http://127.0.0.1:8080/hook",
		"http://localhost/hook",
		"http://10.0.0.5/hook",
		"http://192.168.1.10/hook",
		"http://172.16.0.1/hook",
		"http://[::1]/hook",
		// Los metadatos de la nube, que es a donde apunta el que busca
		// credenciales.
		"http://169.254.169.254/latest/meta-data/",
		"http://100.64.0.1/hook",
	} {
		if err := ValidarWebhook(malo); err == nil {
			t.Errorf("se aceptó %q", malo)
		}
	}
	for _, otro := range []string{"ftp://x/y", "sin-esquema.com/hook", ""} {
		if err := ValidarWebhook(otro); err == nil {
			t.Errorf("se aceptó %q", otro)
		}
	}
}

// Y en una máquina de desarrollo se puede permitir, porque si no no hay forma
// de probarlo.
func TestSePuedenPermitirLasPrivadasParaProbar(t *testing.T) {
	t.Setenv(permitirPrivadasVar, "1")
	if err := ValidarWebhook("http://127.0.0.1:5678/webhook/x"); err != nil {
		t.Errorf("con la variable puesta igual se rechazó: %v", err)
	}
}

func TestMandarElAviso(t *testing.T) {
	t.Setenv(permitirPrivadasVar, "1")
	var recibido Aviso
	var tipo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tipo = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&recibido)
	}))
	defer srv.Close()

	aviso := Aviso{
		Alerta: "ENACOM", Fuente: FuenteNacional,
		Novedades: []Coincidencia{{ID: "7", Titulo: "Resolución 1"}},
		Total:     1,
	}
	if err := Mandar(context.Background(), ClientePorDefecto(), srv.URL, aviso); err != nil {
		t.Fatal(err)
	}
	if recibido.Alerta != "ENACOM" || len(recibido.Novedades) != 1 {
		t.Errorf("llegó %+v", recibido)
	}
	if !strings.HasPrefix(tipo, "application/json") {
		t.Errorf("content-type = %q", tipo)
	}
}

// Un destino que contesta mal tiene que quedar anotado como error de esa
// alerta, no romper la pasada.
func TestUnDestinoQueFallaSeInforma(t *testing.T) {
	t.Setenv(permitirPrivadasVar, "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := Mandar(context.Background(), ClientePorDefecto(), srv.URL, Aviso{})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v", err)
	}
}
