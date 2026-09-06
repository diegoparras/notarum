package alertas

import (
	"testing"
	"time"
)

func alertaDePrueba() Alerta {
	return Alerta{
		Nombre: "ENACOM", Fuente: FuenteNacional, Activa: true,
		Criterios: Criterios{Texto: "enacom"},
	}
}

func TestUnaAlertaSinCriteriosNoSeGuarda(t *testing.T) {
	a := Alerta{Nombre: "todo", Fuente: FuenteNacional}
	if err := a.Validar(); err == nil {
		t.Error("se aceptó una alerta que coincide con todo")
	}
	sinNombre := alertaDePrueba()
	sinNombre.Nombre = "  "
	if err := sinNombre.Validar(); err == nil {
		t.Error("se aceptó una alerta sin nombre")
	}
	mala := alertaDePrueba()
	mala.Fuente = "inventada"
	if err := mala.Validar(); err == nil {
		t.Error("se aceptó una fuente que no existe")
	}
	buena := alertaDePrueba()
	if err := buena.Validar(); err != nil {
		t.Errorf("se rechazó una alerta razonable: %v", err)
	}
}

// La primera pasada no avisa nada: estrenar una alerta sobre un tema viejo
// mandaría de golpe todo lo que existe desde 1993.
func TestLaPrimeraPasadaNoAvisa(t *testing.T) {
	a := alertaDePrueba()
	coincidencias := []Coincidencia{{ID: "1"}, {ID: "2"}}

	nuevas, vistos := a.Novedades(coincidencias)
	if len(nuevas) != 0 {
		t.Errorf("la primera pasada avisó de %d", len(nuevas))
	}
	if len(vistos) != 2 {
		t.Errorf("no se anotó lo que ya estaba: %v", vistos)
	}
}

func TestSoloAvisaLoNuevo(t *testing.T) {
	a := alertaDePrueba()
	a.UltimaCorrida = time.Now().Add(-24 * time.Hour)
	a.Vistos = []string{"1", "2"}

	nuevas, vistos := a.Novedades([]Coincidencia{{ID: "1"}, {ID: "2"}, {ID: "3"}})
	if len(nuevas) != 1 || nuevas[0].ID != "3" {
		t.Errorf("avisó de %v", nuevas)
	}
	if len(vistos) != 3 {
		t.Errorf("vistos = %v", vistos)
	}

	// Y en la pasada siguiente, sin nada nuevo, no avisa de vuelta: una alerta
	// que repite lo mismo todos los días se ignora a la semana.
	a.Vistos = vistos
	otra, _ := a.Novedades([]Coincidencia{{ID: "1"}, {ID: "2"}, {ID: "3"}})
	if len(otra) != 0 {
		t.Errorf("repitió %v", otra)
	}
}

// Lo que deja de coincidir se olvida: si vuelve a aparecer, volver a avisarlo
// es lo correcto.
func TestLoQueDejaDeCoincidirSeOlvida(t *testing.T) {
	a := alertaDePrueba()
	a.UltimaCorrida = time.Now()
	a.Vistos = []string{"1", "2", "3"}

	_, vistos := a.Novedades([]Coincidencia{{ID: "2"}})
	if len(vistos) != 1 || vistos[0] != "2" {
		t.Errorf("vistos = %v", vistos)
	}
}

func TestNoSeGuardanMasVistosQueElTope(t *testing.T) {
	a := alertaDePrueba()
	a.UltimaCorrida = time.Now()
	var muchas []Coincidencia
	for i := 0; i < MaximoVistos+500; i++ {
		muchas = append(muchas, Coincidencia{ID: string(rune('a'+i%26)) + string(rune(i))})
	}
	_, vistos := a.Novedades(muchas)
	if len(vistos) != MaximoVistos {
		t.Errorf("se guardaron %d vistos", len(vistos))
	}
}
