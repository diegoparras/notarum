package main

import (
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
)

// La semana que se baja es siempre la última completa, de lunes a viernes.
//
// Corre el sábado, pero tiene que dar lo mismo si alguien lo lanza a mano un
// martes: si dependiera del día en que se corre, una corrida manual bajaría
// una semana distinta que la automática y nadie sabría cuál falta.
func TestLaSemanaAnteriorEsSiempreLaUltimaCompleta(t *testing.T) {
	// La semana del lunes 31/08/2026 al viernes 04/09/2026.
	esperado := struct{ lunes, viernes string }{"2026-08-31", "2026-09-04"}

	for _, cuando := range []struct {
		dia   string
		fecha time.Time
	}{
		{"sábado", time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)},
		{"domingo", time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)},
		{"lunes siguiente", time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)},
		{"martes siguiente", time.Date(2026, 9, 8, 15, 0, 0, 0, time.UTC)},
		{"viernes siguiente", time.Date(2026, 9, 11, 23, 0, 0, 0, time.UTC)},
	} {
		l, v := semanaAnterior(boletin.Fecha{Time: cuando.fecha})
		if l.API() != esperado.lunes || v.API() != esperado.viernes {
			t.Errorf("corriendo un %s: del %s al %s, se esperaba del %s al %s",
				cuando.dia, l.API(), v.API(), esperado.lunes, esperado.viernes)
		}
	}
}

// Y son cinco días: el Boletín no publica sábados ni domingos, así que pedirle
// siete sería pedirle dos que nunca van a estar.
func TestLaSemanaEsDeLunesAViernes(t *testing.T) {
	l, v := semanaAnterior(boletin.Fecha{Time: time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)})
	if l.Weekday() != time.Monday {
		t.Errorf("empieza un %s", l.Weekday())
	}
	if v.Weekday() != time.Friday {
		t.Errorf("termina un %s", v.Weekday())
	}
	if dias := int(v.Sub(l.Time).Hours()/24) + 1; dias != 5 {
		t.Errorf("son %d días", dias)
	}
}
