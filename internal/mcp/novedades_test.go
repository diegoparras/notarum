package mcp

import (
	"strings"
	"testing"
)

// Las herramientas nuevas tienen que estar en la lista: una herramienta que
// existe pero no se anuncia no la usa nadie, porque el modelo no sabe que está.
func TestLasHerramientasNuevasSeAnuncian(t *testing.T) {
	tiene := map[string]bool{}
	for _, h := range Herramientas() {
		tiene[h.Nombre] = true
		if h.Descripcion == "" || h.Esquema == nil {
			t.Errorf("la herramienta %s se anuncia sin descripción o sin esquema", h.Nombre)
		}
	}
	for _, quiero := range []string{
		"buscar_todo", "novedades", "nacional_relaciones",
		"nacional_buscar", "provincial_buscar",
	} {
		if !tiene[quiero] {
			t.Errorf("falta la herramienta %s", quiero)
		}
	}
}

// Lo que llega mal se rechaza con una explicación, no con un resultado vacío:
// un modelo que recibe una lista vacía concluye que no hay nada.
func TestLasHerramientasNuevasExplicanLoQueFalta(t *testing.T) {
	s := servidorDePrueba(t, false)

	for _, caso := range []struct {
		herramienta string
		args        map[string]any
		esperado    string
	}{
		{"buscar_todo", map[string]any{}, "texto"},
		{"novedades", map[string]any{"desde": "2026-09-01"}, "fuente"},
		{"novedades", map[string]any{"fuente": "nacional"}, "desde"},
		{"novedades", map[string]any{"fuente": "nacional", "desde": "ayer"}, "AAAA-MM-DD"},
		{"novedades", map[string]any{"fuente": "inventada", "desde": "2026-09-01"}, "nacional o provincial"},
	} {
		r := llamarHerramienta(t, s, caso.herramienta, caso.args)
		if !r.EsError {
			t.Errorf("%s con %v no dio error", caso.herramienta, caso.args)
			continue
		}
		if len(r.Contenido) == 0 || !strings.Contains(r.Contenido[0].Texto, caso.esperado) {
			t.Errorf("%s con %v dijo %q, se esperaba algo con %q",
				caso.herramienta, caso.args, r.Contenido, caso.esperado)
		}
	}
}
