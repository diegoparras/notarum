package almacen

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
)

// Los tests de Postgres necesitan una base de verdad. Se corren así:
//
//	docker run -d --name pg -e POSTGRES_PASSWORD=notarum -e POSTGRES_USER=notarum \
//	  -e POSTGRES_DB=notarum -p 55432:5432 postgres:16-alpine
//	NOTARUM_TEST_POSTGRES='postgres://notarum:notarum@localhost:55432/notarum?sslmode=disable' go test ./internal/almacen/
//
// Sin esa variable se saltean, para que la suite corra en cualquier máquina.
// El CI la define y los corre de verdad.
func dsnDePrueba(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("NOTARUM_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("sin NOTARUM_TEST_POSTGRES: se saltean los tests de Postgres")
	}
	return dsn
}

// nuevoPostgresDePrueba da una base limpia: cada test corre en su propio
// esquema y lo borra al final, así no se pisan entre ellos.
var contadorEsquema int

func nuevoPostgresDePrueba(t *testing.T) *Postgres {
	t.Helper()
	dsn := dsnDePrueba(t)
	contadorEsquema++
	esquema := fmt.Sprintf("prueba_%d_%d", time.Now().UnixNano()%1000000, contadorEsquema)

	p, err := NuevoPostgres(OpcionesPostgres{DSN: dsn, Esquema: esquema})
	if err != nil {
		t.Fatalf("no se pudo abrir Postgres: %v", err)
	}
	t.Cleanup(func() {
		_, _ = p.db.Exec("DROP SCHEMA IF EXISTS " + esquema + " CASCADE")
		p.Cerrar()
	})
	return p
}

// Postgres tiene que pasar exactamente el mismo contrato que los otros dos.
func TestConformidadPostgres(t *testing.T) {
	probarAlmacen(t, func(t *testing.T) Almacen { return nuevoPostgresDePrueba(t) })
	probarIndexador(t, func(t *testing.T) Indexador { return nuevoPostgresDePrueba(t) })
}

// El diccionario de castellano de Postgres hace stemming: buscar "promulgar"
// tiene que encontrar "Promúlgase". SQLite no puede hacer esto.
func TestPostgresEntiendeCastellano(t *testing.T) {
	p := nuevoPostgresDePrueba(t)
	if err := p.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")

	// El aviso dice "Promúlgase": todas estas formas tienen que encontrarlo.
	// Buscar "promulgar" y dar con "Promúlgase" es stemming de verdad, algo
	// que el índice de SQLite no puede hacer.
	for _, texto := range []string{"promúlgase", "promulgase", "promulgar", "PROMULGA"} {
		res, err := p.BuscarLocal(ConsultaLocal{Texto: texto, Desde: desde, Hasta: hasta})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 1 {
			t.Errorf("buscando %q: total = %d, se esperaba 1", texto, res.Total)
		}
	}
	// El aviso dice "Designaciones", y la forma bien escrita lo encuentra.
	for _, texto := range []string{"designaciones", "designación", "designar"} {
		res, err := p.BuscarLocal(ConsultaLocal{Texto: texto, Desde: desde, Hasta: hasta})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 1 {
			t.Errorf("buscando %q: total = %d, se esperaba 1", texto, res.Total)
		}
	}
}

// Límite conocido, documentado a propósito: como unaccent corre antes que el
// stemmer, una palabra que en castellano lleva tilde y se escribe sin ella
// pierde el stemming. "designacion" no encuentra "Designaciones", aunque
// "designación" y "designaciones" sí.
//
// Es el precio de que "energia" encuentre "ENERGÍA", que es el caso mucho más
// frecuente. El índice de SQLite tiene el mismo límite y además sin stemming.
// Si algún día molesta, la salida es indexar las dos formas.
func TestPostgresLimiteDelStemmerSinTildes(t *testing.T) {
	p := nuevoPostgresDePrueba(t)
	if err := p.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")

	res, err := p.BuscarLocal(ConsultaLocal{Texto: "designacion", Desde: desde, Hasta: hasta})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 {
		t.Logf("el límite del stemmer cambió: 'designacion' ahora encuentra %d avisos."+
			" Si es por una mejora, actualizá este test y el comentario.", res.Total)
	}
}

// Un esquema con nombre raro no puede colarse como SQL.
func TestPostgresRechazaEsquemasPeligrosos(t *testing.T) {
	for _, esquema := range []string{
		"public; DROP TABLE avisos;--",
		"con espacio",
		`"comillas"`,
		"MAYUSCULAS",
		"1empieza_con_numero",
		"",
	} {
		if esquema == "" {
			continue // vacío significa "public", que es válido
		}
		if esIdentificadorSeguro(esquema) {
			t.Errorf("se aceptó el esquema %q", esquema)
		}
	}
	for _, ok := range []string{"public", "notarum", "esquema_1", "n"} {
		if !esIdentificadorSeguro(ok) {
			t.Errorf("se rechazó el esquema válido %q", ok)
		}
	}
}

func TestPostgresSinDSN(t *testing.T) {
	if _, err := NuevoPostgres(OpcionesPostgres{}); err == nil {
		t.Error("se aceptó una configuración sin cadena de conexión")
	}
}

// Dos instancias contra la misma base ven lo mismo: es la razón de usar
// Postgres en vez de un archivo local.
func TestPostgresCompartidoEntreInstancias(t *testing.T) {
	dsn := dsnDePrueba(t)
	esquema := fmt.Sprintf("compartido_%d", time.Now().UnixNano()%1000000)

	uno, err := NuevoPostgres(OpcionesPostgres{DSN: dsn, Esquema: esquema})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = uno.db.Exec("DROP SCHEMA IF EXISTS " + esquema + " CASCADE")
		uno.Cerrar()
	}()

	dos, err := NuevoPostgres(OpcionesPostgres{DSN: dsn, Esquema: esquema})
	if err != nil {
		t.Fatal(err)
	}
	defer dos.Cerrar()

	if err := uno.Guardar("ediciones/primera/2026-09-01", []byte(`{"cantidad":100}`), SinVencimiento); err != nil {
		t.Fatal(err)
	}
	datos, ok := dos.Leer("ediciones/primera/2026-09-01")
	if !ok {
		t.Fatal("la segunda instancia no vio lo que guardó la primera")
	}
	if string(datos) != `{"cantidad":100}` {
		t.Errorf("datos = %s", datos)
	}

	// Y lo mismo con el índice.
	if err := uno.IndexarEdicion(edicionDePrueba(t)); err != nil {
		t.Fatal(err)
	}
	desde, _ := boletin.ParseFecha("2026-01-01")
	hasta, _ := boletin.ParseFecha("2026-12-31")
	res, err := dos.BuscarLocal(ConsultaLocal{Texto: "aduana", Desde: desde, Hasta: hasta})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Errorf("la segunda instancia no encontró lo que indexó la primera: %d", res.Total)
	}
}
