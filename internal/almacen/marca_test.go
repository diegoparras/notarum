package almacen

import (
	"testing"
)

func TestLaPrimeraVezLaMarcaEsNueva(t *testing.T) {
	a, err := NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Cerrar()

	m, err := Marcar(a)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Nueva {
		t.Error("el almacén estaba vacío y la marca no dice que es nueva")
	}
	if m.Arranques != 1 {
		t.Errorf("arranques = %d", m.Arranques)
	}
}

// Lo que importa: si el almacén sobrevive, el segundo arranque tiene que
// encontrar la marca del primero. Si no la encuentra, los datos se perdieron.
func TestElSegundoArranqueEncuentraLaMarcaDelPrimero(t *testing.T) {
	dir := t.TempDir()
	a, err := NuevoDisco(dir)
	if err != nil {
		t.Fatal(err)
	}
	primera, err := Marcar(a)
	if err != nil {
		t.Fatal(err)
	}
	a.Cerrar()

	// Otro proceso, el mismo directorio: es lo que pasa en un redespliegue
	// con el volumen bien montado.
	b, err := NuevoDisco(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Cerrar()

	segunda, err := Marcar(b)
	if err != nil {
		t.Fatal(err)
	}
	if segunda.Nueva {
		t.Error("dice que el almacén estaba vacío y tenía la marca del arranque anterior")
	}
	if segunda.Arranques != 2 {
		t.Errorf("arranques = %d", segunda.Arranques)
	}
	if !segunda.Desde.Equal(primera.Desde) {
		t.Errorf("la fecha de origen cambió: %s -> %s", primera.Desde, segunda.Desde)
	}
}

// Y si el directorio no es el mismo —el volumen que no está montado— la marca
// vuelve a ser nueva, que es lo que hay que poder decir.
func TestUnAlmacenDistintoVuelveAEmpezar(t *testing.T) {
	a, _ := NuevoDisco(t.TempDir())
	defer a.Cerrar()
	if _, err := Marcar(a); err != nil {
		t.Fatal(err)
	}

	b, _ := NuevoDisco(t.TempDir())
	defer b.Cerrar()
	m, err := Marcar(b)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Nueva {
		t.Error("otro almacén, y no dice que arrancó vacío")
	}
}
