package almacen

import (
	"strconv"
	"testing"
)

// Todos los motores tienen que guardar por lotes lo mismo que de a una, y lo
// que queda en el último lote sin llenar tiene que guardarse igual: sin eso,
// hasta mil normas se leerían y no se guardarían, en silencio.
func TestGuardarPorLotes(t *testing.T) {
	paraCadaMotor(t, func(t *testing.T, a Almacen) {
		ac := NuevoAcumulador(a)
		const cuantas = TamañoDeLote + 7 // uno y medio: obliga a vaciar el resto
		for i := 0; i < cuantas; i++ {
			if err := ac.Sumar("lote/"+strconv.Itoa(i), []byte(strconv.Itoa(i)), SinVencimiento); err != nil {
				t.Fatalf("en la %d: %v", i, err)
			}
		}
		if err := ac.Vaciar(); err != nil {
			t.Fatal(err)
		}
		for _, i := range []int{0, TamañoDeLote - 1, TamañoDeLote, cuantas - 1} {
			crudo, hay := a.Leer("lote/" + strconv.Itoa(i))
			if !hay {
				t.Errorf("no se guardó la %d", i)
				continue
			}
			if string(crudo) != strconv.Itoa(i) {
				t.Errorf("la %d quedó como %q", i, crudo)
			}
		}
	})
}

// Un lote con una clave vacía no puede dejar guardado a medias: o entra todo o
// no entra nada, que es para lo que existe una transacción.
func TestUnLoteConUnErrorNoDejaLaMitad(t *testing.T) {
	paraCadaMotor(t, func(t *testing.T, a Almacen) {
		l, sabe := a.(PorLotes)
		if !sabe {
			t.Skip("este motor no guarda por lotes")
		}
		err := l.GuardarLote([]Entrada{
			{Clave: "bueno/1", Datos: []byte("1")},
			{Clave: "  ", Datos: []byte("2")},
		})
		if err == nil {
			t.Fatal("se aceptó un lote con una clave vacía")
		}
		if _, hay := a.Leer("bueno/1"); hay {
			t.Error("quedó guardada la parte de antes del error")
		}
	})
}

// paraCadaMotor corre lo mismo contra los motores que haya disponibles: lo que
// vale para uno tiene que valer para todos, o dejan de ser intercambiables.
func paraCadaMotor(t *testing.T, probar func(*testing.T, Almacen)) {
	t.Helper()
	t.Run("disco", func(t *testing.T) {
		a, err := NuevoDisco(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer a.Cerrar()
		probar(t, a)
	})
	t.Run("sqlite", func(t *testing.T) {
		a := nuevaSQLite(t)
		probar(t, a)
	})
	t.Run("postgres", func(t *testing.T) {
		a := nuevoPostgresDePrueba(t) // salta solo si no hay Postgres configurado
		probar(t, a)
	})
}
