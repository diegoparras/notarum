package almacen

import (
	"testing"
	"time"
)

func nueva(t *testing.T) *Disco {
	t.Helper()
	d, err := NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestGuardarYLeer(t *testing.T) {
	d := nueva(t)
	if _, ok := d.Leer("ediciones/primera/2026-09-01"); ok {
		t.Fatal("la caché vacía devolvió un acierto")
	}
	if err := d.Guardar("ediciones/primera/2026-09-01", []byte(`{"a":1}`), SinVencimiento); err != nil {
		t.Fatal(err)
	}
	datos, ok := d.Leer("ediciones/primera/2026-09-01")
	if !ok {
		t.Fatal("no se leyó lo que se acababa de guardar")
	}
	if string(datos) != `{"a":1}` {
		t.Errorf("datos = %s", datos)
	}
}

func TestVencimiento(t *testing.T) {
	d := nueva(t)
	if err := d.Guardar("hoy", []byte(`{"a":1}`), 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Leer("hoy"); !ok {
		t.Fatal("debería estar vigente")
	}
	time.Sleep(200 * time.Millisecond)
	if _, ok := d.Leer("hoy"); ok {
		t.Error("la entrada venció y se devolvió igual")
	}
	if d.Existe("hoy") {
		t.Error("Existe devolvió true para una entrada vencida")
	}
}

func TestSinVencimientoNoCaduca(t *testing.T) {
	d := nueva(t)
	if err := d.Guardar("pasada", []byte(`1`), SinVencimiento); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, ok := d.Leer("pasada"); !ok {
		t.Error("una edición pasada no debería caducar")
	}
}

// Una clave no puede escribir fuera del directorio de caché.
func TestClaveNoEscapaDelDirectorio(t *testing.T) {
	d := nueva(t)
	for _, clave := range []string{"../fuera", "a/../../fuera", "/../../etc/passwd", ""} {
		if err := d.Guardar(clave, []byte(`1`), SinVencimiento); err == nil {
			t.Errorf("la clave %q se aceptó y debería rechazarse", clave)
		}
		if _, ok := d.Leer(clave); ok {
			t.Errorf("la clave %q devolvió datos", clave)
		}
	}
}

func TestMetricas(t *testing.T) {
	d := nueva(t)
	d.Leer("no-esta")
	_ = d.Guardar("esta", []byte(`1`), SinVencimiento)
	d.Leer("esta")
	m := d.Metricas()
	if m.Aciertos != 1 || m.Fallos != 1 || m.Escritos != 1 || m.Entradas != 1 {
		t.Errorf("metricas = %+v", m)
	}
}

func TestBorrar(t *testing.T) {
	d := nueva(t)
	_ = d.Guardar("x", []byte(`1`), SinVencimiento)
	if err := d.Borrar("x"); err != nil {
		t.Fatal(err)
	}
	if d.Existe("x") {
		t.Error("la entrada sigue después de borrarla")
	}
	if err := d.Borrar("x"); err != nil {
		t.Errorf("borrar dos veces debería ser inocuo: %v", err)
	}
}
