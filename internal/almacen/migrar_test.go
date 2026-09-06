package almacen

import (
	"strconv"
	"testing"
	"time"
)

// Cambiar de motor no puede costar volver a bajar los catálogos: son cientos
// de miles de normas y varios minutos de un portal que no es nuestro.
func TestMigrarDeUnMotorAOtro(t *testing.T) {
	viejo := nuevaSQLite(t)
	nuevo, err := NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer nuevo.Cerrar()

	const cuantas = TamañoDeLote + 13
	for i := 0; i < cuantas; i++ {
		if err := viejo.Guardar("normas/"+strconv.Itoa(i), []byte(strconv.Itoa(i)), SinVencimiento); err != nil {
			t.Fatal(err)
		}
	}
	// Y una edición, que después hay que poder reindexar.
	if err := viejo.Guardar("ediciones/primera/2026-09-01", []byte(`{"a":1}`), SinVencimiento); err != nil {
		t.Fatal(err)
	}

	a, err := Migrar(viejo, nuevo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Copiadas != cuantas+1 {
		t.Errorf("copió %d y había %d", a.Copiadas, cuantas+1)
	}
	// Lo del último lote sin llenar también: sin vaciarlo se perderían hasta
	// mil entradas, en silencio.
	for _, i := range []int{0, TamañoDeLote, cuantas - 1} {
		crudo, hay := nuevo.Leer("normas/" + strconv.Itoa(i))
		if !hay || string(crudo) != strconv.Itoa(i) {
			t.Errorf("la %d no llegó bien: %q, %v", i, crudo, hay)
		}
	}

	// Las ediciones se listan aparte, para poder rearmar el índice del motor
	// nuevo en vez de traducir el del viejo.
	eds, err := Ediciones(nuevo)
	if err != nil {
		t.Fatal(err)
	}
	if len(eds) != 1 || eds[0] != "ediciones/primera/2026-09-01" {
		t.Errorf("ediciones = %v", eds)
	}
}

// Lo vencido no se copia: arrastrarlo al motor nuevo sería revivir cosas que
// el viejo ya había dado por muertas.
func TestLoVencidoNoSeMigra(t *testing.T) {
	viejo := nuevaSQLite(t)
	nuevo, err := NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer nuevo.Cerrar()

	viejo.Guardar("queda", []byte(`"si"`), SinVencimiento)
	// Con la fecha de vencimiento en el pasado, escrita directo: un TTL
	// negativo se toma como "sin vencimiento", así que no sirve para esto.
	if _, err := viejo.db.Exec(
		`INSERT INTO entradas (clave, datos, guardado_en, vence_en) VALUES (?, ?, ?, ?)`,
		"vencida", []byte(`"no"`), time.Now().Add(-time.Hour).UnixMilli(),
		time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	if _, err := Migrar(viejo, nuevo, nil); err != nil {
		t.Fatal(err)
	}
	if _, hay := nuevo.Leer("queda"); !hay {
		t.Error("no se copió lo que valía")
	}
	if _, hay := nuevo.Leer("vencida"); hay {
		t.Error("se copió una entrada vencida")
	}
}

// Migrar sobre sí mismo no tiene sentido y es fácil de pedir por error.
func TestNoSeMigraSobreSiMismo(t *testing.T) {
	a := nuevaSQLite(t)
	if _, err := Migrar(a, a, nil); err == nil {
		t.Error("se aceptó migrar un almacén sobre sí mismo")
	}
}
