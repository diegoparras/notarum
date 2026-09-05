// Package memoria hace que el proceso se entere de cuánta memoria le dio el
// contenedor.
//
// Go no lo mira solo: por defecto deja crecer el heap hasta el doble de lo
// vivo, sin saber que afuera hay un límite. En un contenedor con 512 MB, eso
// termina en un OOM del que el proceso ni se entera —lo mata el sistema— y
// desde afuera se ve como el servicio que deja de responder sin explicación.
//
// Armar el índice de InfoLEG es justo ese caso: los datos son 110 MB, pero
// leer 256 MB de CSV para armarlos hace un pico varias veces mayor. Con el
// límite puesto, el recolector trabaja más seguido y el pico no llega.
package memoria

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// margen es lo que se le deja al proceso por fuera del heap: los buffers de
// red, las pilas, lo que reserva SQLite. Poner el límite igual al del
// contenedor sería pedirle al recolector que evite un OOM que ya empezó.
const margen = 0.85

// Ajustar fija el límite de memoria del recolector a partir de lo que diga el
// contenedor, o de lo que se configure a mano.
//
// Devuelve los bytes que quedaron fijados, o 0 si no había límite que
// respetar —correr sin contenedor, o con recursos ilimitados—, que es cuando
// conviene dejar a Go con su comportamiento de siempre.
func Ajustar(configurado string) int64 {
	if v := strings.TrimSpace(configurado); v != "" {
		if bytes, err := enBytes(v); err == nil && bytes > 0 {
			debug.SetMemoryLimit(bytes)
			return bytes
		}
	}
	limite := DelContenedor()
	if limite <= 0 {
		return 0
	}
	conMargen := int64(float64(limite) * margen)
	debug.SetMemoryLimit(conMargen)
	return conMargen
}

// DelContenedor lee el límite de memoria que impone el cgroup, o 0 si no hay.
//
// Se leen los dos formatos porque conviven: cgroup v2 es lo que usan los
// sistemas nuevos y v1 sigue en pie en muchos lados.
func DelContenedor() int64 {
	// cgroup v2.
	if crudo, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if l := leerLimite(string(crudo)); l > 0 {
			return l
		}
	}
	// cgroup v1.
	if crudo, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		if l := leerLimite(string(crudo)); l > 0 {
			return l
		}
	}
	return 0
}

// leerLimite entiende lo que hay en esos archivos: un número, o "max" cuando
// no hay tope. Los sistemas sin límite ponen un número enorme, que hay que
// tratar como si no hubiera ninguno.
func leerLimite(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	// Más de un terabyte es la forma que tiene el sistema de decir "sin
	// límite": nadie le da eso a un contenedor de verdad.
	if n > 1<<40 {
		return 0
	}
	return n
}

// enBytes lee un tamaño escrito como se escribe: 512MB, 1GB, 1.5g, o los
// bytes pelados.
func enBytes(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	multiplicadores := []struct {
		sufijo string
		veces  int64
	}{
		// De más largo a más corto: "GIB" tiene que ganarle a "B".
		{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10},
		{"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10},
		{"B", 1},
	}
	veces := int64(1)
	for _, m := range multiplicadores {
		if strings.HasSuffix(s, m.sufijo) {
			s, veces = strings.TrimSuffix(s, m.sufijo), m.veces
			break
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, err
	}
	return int64(n * float64(veces)), nil
}
