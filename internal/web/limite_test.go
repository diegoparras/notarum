package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
)

// Quien lee el Boletín desde el navegador no tiene por qué saber qué es un
// 429: tiene que entender que no es un error suyo, que lo guardado sigue
// estando, y cuánto esperar.
func TestElLectorExplicaQueElSitioEstaLimitando(t *testing.T) {
	casos := []struct {
		falta  time.Duration
		espera string
	}{
		{20 * time.Second, "en un minuto"},
		{4*time.Minute + 10*time.Second, "en unos 5 minutos"},
		{59*time.Minute + 30*time.Second, "en una hora"},
	}
	for _, c := range casos {
		lim := &boletin.ErrLimitado{Operacion: "leer aviso", Hasta: time.Now().Add(c.falta), SinPedir: true}
		// Envuelto, como llega desde el servicio.
		msg := mensajeDeError(fmt.Errorf("no se pudo: %w", lim))
		if !strings.Contains(msg, "más despacio") {
			t.Errorf("falta %v: el mensaje no explica el motivo: %q", c.falta, msg)
		}
		if !strings.Contains(msg, "guardado") {
			t.Errorf("falta %v: el mensaje no dice que lo guardado se sigue viendo: %q", c.falta, msg)
		}
		if !strings.Contains(msg, c.espera) {
			t.Errorf("falta %v: se esperaba %q en %q", c.falta, c.espera, msg)
		}
		if strings.Contains(msg, "429") {
			t.Errorf("el mensaje del lector no lleva códigos: %q", msg)
		}
	}
}
