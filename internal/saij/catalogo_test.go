package saij

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// El fixture son 48 filas del CSV real, elegidas para cubrir lo raro:
// constituciones, códigos, normas de facto, filas sin título, fechas del
// siglo XIX y de 2026, y 17 provincias.
func abrirFixture(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "normativa_provincial.csv"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func leerTodas(t *testing.T) []Norma {
	t.Helper()
	var normas []Norma
	n, err := LeerCatalogo(abrirFixture(t), func(x Norma) error {
		normas = append(normas, x)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != len(normas) {
		t.Fatalf("dice haber leído %d y entregó %d", n, len(normas))
	}
	return normas
}

func TestLeerCatalogo(t *testing.T) {
	normas := leerTodas(t)
	if len(normas) != 48 {
		t.Fatalf("leídas %d, el fixture tiene 48", len(normas))
	}
	for i, n := range normas {
		if n.ID == "" {
			t.Errorf("la fila %d no tiene identificador", i)
		}
		if n.Provincia == "" {
			t.Errorf("%s no tiene provincia", n.ID)
		}
		if n.Tipo == "" {
			t.Errorf("%s no tiene tipo", n.ID)
		}
		// Titulo() nunca puede volver vacío: es lo que se muestra en la lista.
		if n.Titulo() == "" {
			t.Errorf("%s no tiene con qué mostrarse", n.ID)
		}
	}
}

// Los identificadores son la clave, así que no se pueden repetir.
func TestLosIdentificadoresNoSeRepiten(t *testing.T) {
	vistos := map[string]bool{}
	for _, n := range leerTodas(t) {
		if vistos[n.ID] {
			t.Errorf("el identificador %s aparece dos veces", n.ID)
		}
		vistos[n.ID] = true
	}
}

// La tabla de provincias tiene que coincidir con lo que trae la base: el
// prefijo de cada identificador es el de su provincia.
func TestLaTablaDeProvinciasCoincideConLaBase(t *testing.T) {
	porID := map[string]Provincia{}
	for _, p := range Provincias {
		porID[p.ID] = p
	}
	for _, n := range leerTodas(t) {
		p, hay := porID[n.ProvinciaID]
		if !hay {
			t.Errorf("%s trae la provincia %q (%s), que no está en la tabla",
				n.ID, n.Provincia, n.ProvinciaID)
			continue
		}
		if p.Nombre != n.Provincia {
			t.Errorf("%s: la base la llama %q y la tabla %q", n.ID, n.Provincia, p.Nombre)
		}
		if !strings.HasPrefix(n.ID, p.Prefijo) {
			t.Errorf("%s es de %s, cuyo prefijo dice ser %s", n.ID, p.Nombre, p.Prefijo)
		}
	}
}

func TestIdentificador(t *testing.T) {
	for entrada, esperado := range map[string]string{
		"www.saij.gob.ar/LPB1000000":         "LPB1000000",
		"https://www.saij.gob.ar/LPH0006109": "LPH0006109",
		"LPZ0002347":                         "LPZ0002347",
		"lpz0002347":                         "LPZ0002347",
		"  www.saij.gob.ar/LPA0004813  ":     "LPA0004813",
		"":                                   "",
		"www.saij.gob.ar/":                   "",
		"www.saij.gob.ar/con espacio":        "",
		"www.saij.gob.ar/LPB-1000000":        "",
		"www.saij.gob.ar/..":                 "",
	} {
		if got := identificador(entrada); got != esperado {
			t.Errorf("%q -> %q, se esperaba %q", entrada, got, esperado)
		}
	}
}

// El identificador se usa para armar una dirección y una clave de almacén, y
// llega desde un archivo que baja de internet. Nada de lo que salga de acá
// puede tener con qué salirse de su lugar.
func TestElIdentificadorNoSePuedeEscapar(t *testing.T) {
	sospechosos := []string{
		"www.saij.gob.ar/../../etc/passwd",
		"www.saij.gob.ar/..%2f..%2fetc",
		"www.saij.gob.ar/<script>alert(1)</script>",
		"www.saij.gob.ar/LPB1000000?x=1",
		"www.saij.gob.ar/LPB1000000#frag",
		"www.saij.gob.ar/a b",
		"https://otro-sitio.example/LPB1000000",
		"javascript:alert(1)",
		"www.saij.gob.ar/" + strings.Repeat("A", 200),
	}
	for _, s := range sospechosos {
		id := identificador(s)
		if id == "" {
			continue // rechazado, que es una respuesta correcta
		}
		for _, r := range id {
			esLetra := r >= 'A' && r <= 'Z'
			esDigito := r >= '0' && r <= '9'
			if !esLetra && !esDigito {
				t.Errorf("%q -> %q, que trae %q", s, id, r)
			}
		}
		if len(id) > 32 {
			t.Errorf("%q -> un identificador de %d caracteres", s, len(id))
		}
	}
}

// Un catálogo que cambió de forma se avisa, en vez de guardar 81 mil filas
// vacías.
func TestCatalogoConOtrasColumnas(t *testing.T) {
	otro := "una,dos,tres\n1,2,3\n"
	if _, err := LeerCatalogo(strings.NewReader(otro), func(Norma) error { return nil }); err == nil {
		t.Fatal("se aceptó un CSV con otras columnas")
	}
}

// Las columnas se leen por nombre: si la fuente las reordena, tiene que
// seguir andando.
func TestLasColumnasSeLeenPorNombre(t *testing.T) {
	alReves := "texto_actualizado,provincia_nombre,tipo_norma,numero_norma,fecha,provincia_id\n" +
		"www.saij.gob.ar/LPH0006109,Chaco,Ley,6109,2008-04-16,22\n"
	var n []Norma
	if _, err := LeerCatalogo(strings.NewReader(alReves), func(x Norma) error {
		n = append(n, x)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(n) != 1 {
		t.Fatalf("leídas %d", len(n))
	}
	if n[0].ID != "LPH0006109" || n[0].Provincia != "Chaco" || n[0].Numero != "6109" {
		t.Errorf("norma = %+v", n[0])
	}
}

// Una fila sin identificador no sirve para nada: se saltea.
func TestFilaSinIdentificador(t *testing.T) {
	csv := "texto_actualizado,provincia_nombre,tipo_norma\n" +
		",Chaco,Ley\n" +
		"www.saij.gob.ar/LPH0006109,Chaco,Ley\n"
	var n []Norma
	leidas, err := LeerCatalogo(strings.NewReader(csv), func(x Norma) error {
		n = append(n, x)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if leidas != 1 || len(n) != 1 {
		t.Errorf("leídas %d, entregadas %d; la fila sin id tendría que saltearse", leidas, len(n))
	}
}

// Quien lee puede cortar.
func TestSePuedeCortar(t *testing.T) {
	var n int
	_, err := LeerCatalogo(abrirFixture(t), func(Norma) error {
		n++
		if n == 3 {
			return errBasta
		}
		return nil
	})
	if err != errBasta {
		t.Fatalf("err = %v", err)
	}
	if n != 3 {
		t.Errorf("siguió leyendo hasta %d", n)
	}
}

var errBasta = errParaCortar("basta")

type errParaCortar string

func (e errParaCortar) Error() string { return string(e) }
