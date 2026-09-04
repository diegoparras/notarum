package infoleg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func abrirFixture(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "normas.csv"))
	if err != nil {
		t.Fatalf("no se pudo abrir el fixture: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestLeerCatalogo(t *testing.T) {
	var normas []Norma
	n, err := LeerCatalogo(abrirFixture(t), func(x Norma) error {
		normas = append(normas, x)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != len(normas) || n == 0 {
		t.Fatalf("leidas = %d, recolectadas = %d", n, len(normas))
	}
	for _, x := range normas {
		if x.ID <= 0 || x.Tipo == "" {
			t.Errorf("norma incompleta: %+v", x)
		}
	}
}

// Las columnas se leen por nombre: una norma conocida tiene que salir entera.
func TestLeerCatalogoCamposDeUnaNormaConocida(t *testing.T) {
	var ley *Norma
	_, err := LeerCatalogo(abrirFixture(t), func(x Norma) error {
		if x.Numero == "27543" && x.Tipo == "Ley" {
			c := x
			ley = &c
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ley == nil {
		t.Fatal("no se encontró la Ley 27543 en el fixture")
	}
	if ley.ID != 334377 {
		t.Errorf("id = %d, se esperaba 334377", ley.ID)
	}
	if ley.FechaBoletin != "2020-02-12" {
		t.Errorf("fecha_boletin = %q", ley.FechaBoletin)
	}
	if ley.Anio() != 2020 {
		t.Errorf("anio = %d", ley.Anio())
	}
	if !ley.TieneTexto {
		t.Error("la Ley 27543 tiene texto publicado y salió sin él")
	}
	// La URL de esa norma cae en su carpeta de a cinco mil.
	if !strings.Contains(ley.URLTexto(), "/330000-334999/334377/") {
		t.Errorf("url = %q", ley.URLTexto())
	}
}

// Los decretos del Boletín del 20/8/2026 están en el catálogo pero InfoLEG no
// publicó su texto: hay que reflejarlo, no inventar un enlace.
func TestNormasSinTextoPublicado(t *testing.T) {
	vistos := map[string]Norma{}
	_, err := LeerCatalogo(abrirFixture(t), func(x Norma) error {
		if x.Tipo == "Decreto" && x.FechaBoletin == "2026-08-20" {
			vistos[x.Numero] = x
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(vistos) == 0 {
		t.Fatal("el fixture debería tener los decretos del 20/8/2026")
	}
	for numero, x := range vistos {
		if x.TieneTexto {
			t.Errorf("el Decreto %s figura con texto y no lo tiene publicado", numero)
		}
		if x.URLTexto() != "" {
			t.Errorf("el Decreto %s ofreció una URL de texto que no existe", numero)
		}
		if x.URLFicha() == "" {
			t.Errorf("el Decreto %s tiene que ofrecer al menos su ficha", numero)
		}
	}
}

// El catálogo se lee en streaming: cortar a mitad no puede dejar nada colgado.
func TestLeerCatalogoSeCorta(t *testing.T) {
	errCorte := errNoImporta{}
	var contadas int
	n, err := LeerCatalogo(abrirFixture(t), func(Norma) error {
		contadas++
		if contadas == 2 {
			return errCorte
		}
		return nil
	})
	if err != errCorte {
		t.Errorf("err = %v, se esperaba el corte", err)
	}
	if n != 2 {
		t.Errorf("leidas = %d, se esperaban 2", n)
	}
}

type errNoImporta struct{}

func (errNoImporta) Error() string { return "corte" }

// Un CSV que no es el catálogo tiene que decirlo, no devolver cero normas en
// silencio.
func TestLeerCatalogoRechazaOtroCSV(t *testing.T) {
	otro := strings.NewReader("nombre,edad\nana,33\n")
	if _, err := LeerCatalogo(otro, func(Norma) error { return nil }); err == nil {
		t.Error("se aceptó un CSV que no es el catálogo")
	}
}

func TestLeerCatalogoVacio(t *testing.T) {
	if _, err := LeerCatalogo(strings.NewReader(""), func(Norma) error { return nil }); err == nil {
		t.Error("se aceptó un archivo vacío")
	}
}
