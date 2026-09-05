//go:build red

package asistente

import (
	"context"
	"testing"
	"time"
)

// El modelo por defecto tiene que existir en el proveedor de verdad.
//
// Escrito a mano en el código, un nombre de modelo envejece sin avisar:
// OpenRouter agrega y retira modelos, y el día que el nuestro desaparece todas
// las generaciones fallan sin que nada del código haya cambiado. Pasó: el
// anterior era "anthropic/claude-3.5-haiku", que no está en el catálogo, y no
// había forma de enterarse hasta que alguien intentaba generar algo.
//
// Corre con la etiqueta `red`, como los que pegan al sitio del Boletín: no en
// cada push, sino a mano o una vez por día, para enterarse de que el mundo de
// afuera cambió antes de que lo note quien usa el servicio.
func TestElModeloPorDefectoExisteEnElProveedor(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelar()

	// El catálogo es público: no hace falta clave para preguntarle qué ofrece.
	ms, err := NuevoCliente(Opciones{}).Modelos(ctx, "")
	if err != nil {
		t.Fatalf("no se pudo leer el catálogo del proveedor: %v", err)
	}
	for _, m := range ms {
		if m.ID == ModeloPorDefecto {
			t.Logf("%s está: %s, %s", m.ID, m.Nombre, m.Precio())
			return
		}
	}
	t.Errorf("el modelo por defecto %q no está entre los %d que ofrece OpenRouter: "+
		"todas las generaciones que no elijan otro van a fallar", ModeloPorDefecto, len(ms))
}

// Hay modelos de verdad que no aceptan temperature, y notarum tiene que
// poder usarlos: si el proveedor lo ofrece, tiene que entrar.
//
// Este test existe porque la suposición contraria —"todos aceptan lo mismo"—
// hacía fallar la generación entera con cualquier modelo de la familia GPT-5,
// que es de los que alguien va a elegir.
func TestHayModelosQueNoAceptanTemperatura(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelar()

	ms, err := NuevoCliente(Opciones{}).Modelos(ctx, "")
	if err != nil {
		t.Fatalf("no se pudo leer el catálogo del proveedor: %v", err)
	}

	var sinTemperatura, conTemperatura, sinParametros int
	for _, m := range ms {
		switch {
		case len(m.Parametros) == 0:
			sinParametros++
		case m.Acepta("temperature"):
			conTemperatura++
		default:
			sinTemperatura++
		}
	}
	t.Logf("de %d modelos: %d aceptan temperature, %d no, %d no lo declaran",
		len(ms), conTemperatura, sinTemperatura, sinParametros)

	if sinTemperatura == 0 {
		t.Error("ninguno rechaza temperature: o el proveedor cambió cómo lo declara, " +
			"o dejamos de leer supported_parameters")
	}
	if conTemperatura == 0 {
		t.Error("ninguno acepta temperature: se dejó de leer supported_parameters")
	}
}
