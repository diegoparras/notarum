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
