package servicio

import "testing"

// Buscar en el Boletín mira toda la historia indexada. Antes la ventana de
// siete días de las alertas estaba escrita adentro de la búsqueda, y la
// usaban también /v1/todo y buscar_todo del MCP: algo de hace un mes daba
// cero, sin decir por qué.
func TestElBoletinSeBuscaEnTodaLaHistoria(t *testing.T) {
	desde, hasta := rangoDelBoletin(Criterios{Texto: "energía"})
	if !desde.IsZero() || !hasta.IsZero() {
		t.Errorf("una búsqueda común quedó acotada: %v a %v", desde, hasta)
	}
	// Las alertas sí miran sólo lo reciente.
	desde, hasta = rangoDelBoletin(Criterios{UltimosDias: diasQueMiraUnaAlerta})
	if desde.IsZero() || hasta.IsZero() || hasta.Sub(desde.Time).Hours() != float64(24*diasQueMiraUnaAlerta) {
		t.Errorf("la ventana de las alertas: %v a %v", desde, hasta)
	}
	// Y los años van de enero a diciembre.
	desde, hasta = rangoDelBoletin(Criterios{DesdeAnio: 2020, HastaAnio: 2021})
	if desde.API() != "2020-01-01" || hasta.API() != "2021-12-31" {
		t.Errorf("los años: %s a %s", desde.API(), hasta.API())
	}
}
