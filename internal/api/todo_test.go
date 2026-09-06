package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Buscar en las tres de una vez, sin tener que saber en cuál está lo que se
// busca.
func TestBuscarEnLasTresFuentes(t *testing.T) {
	srv := conRelaciones(t) // tiene el catálogo nacional sincronizado
	res, cuerpo := pedir(t, srv, "/v1/todo?texto=consumidor")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("contestó %d: %s", res.StatusCode, cuerpo)
	}
	var r struct {
		Texto      string            `json:"texto"`
		Total      int               `json:"total"`
		PorFuente  map[string]int    `json:"por_fuente"`
		SinMirar   map[string]string `json:"sin_mirar"`
		Resultados []struct {
			Fuente string `json:"fuente"`
			ID     string `json:"id"`
			Enlace string `json:"enlace"`
			EnAPI  string `json:"en_api"`
		} `json:"resultados"`
	}
	if err := json.Unmarshal(cuerpo, &r); err != nil {
		t.Fatalf("no es JSON: %v", err)
	}
	if r.Texto != "consumidor" {
		t.Errorf("texto = %q", r.Texto)
	}

	// Las fuentes que esta instancia no tiene encendidas aparecen con el
	// motivo. Una fuente apagada y una sin resultados llevan a cosas
	// distintas: la primera se arregla, la segunda no.
	for _, fuente := range []string{"boletin", "provincial"} {
		if _, hay := r.SinMirar[fuente]; !hay {
			t.Errorf("la fuente %s no aparece ni con resultados ni en sin_mirar", fuente)
		}
	}
	if r.SinMirar["nacional"] != "" {
		t.Errorf("la nacional está sincronizada y quedó sin mirar: %q", r.SinMirar["nacional"])
	}

	// Y cada resultado dice de dónde salió y dónde verlo.
	for _, x := range r.Resultados {
		if x.Fuente == "" || x.Enlace == "" || x.EnAPI == "" {
			t.Errorf("un resultado vino sin origen o sin enlaces: %+v", x)
		}
		if !strings.HasPrefix(x.ID, x.Fuente+":") {
			t.Errorf("el id %q no lleva su fuente adelante: dos fuentes pueden usar el mismo número", x.ID)
		}
	}
}

func TestBuscarEnTodoNecesitaTexto(t *testing.T) {
	srv := conRelaciones(t)
	res, cuerpo := pedir(t, srv, "/v1/todo")
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("sin texto contestó %d", res.StatusCode)
	}
	if !strings.Contains(string(cuerpo), "texto") {
		t.Errorf("no dice qué falta: %s", cuerpo)
	}
	res, _ = pedir(t, srv, "/v1/todo?texto=x&por_fuente=500")
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("con un por_fuente enorme contestó %d", res.StatusCode)
	}
}
