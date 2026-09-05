//go:build red

// Estos tests pegan al portal de datos real.
//
//	go test ./internal/saij/ -tags red -v
//
// No corren en la suite normal. Sirven para enterarse de que el portal o la
// publicación del catálogo cambiaron, antes de que lo note quien consume.
package saij

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func clienteReal(t *testing.T) *Cliente {
	t.Helper()
	return NuevoCliente(Opciones{
		UserAgent: "notarum-tests/1.2 (+https://github.com/diegoparras/notarum)",
	})
}

// El conjunto sigue publicando un CSV y el portal sigue contestando lo mismo.
func TestRedElCatalogoSiguePublicado(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelar()

	info, err := clienteReal(t).BuscarCatalogo(ctx)
	if err != nil {
		t.Fatalf("no se pudo encontrar el catálogo: %v", err)
	}
	t.Logf("URL: %s", info.URL)
	t.Logf("publicado: %s", info.Modificado.Format(time.RFC3339))

	if info.Modificado.IsZero() {
		t.Error("el portal ya no dice cuándo se publicó; sin eso hay que bajar 28 MB cada vez")
	}
	// Una base que se dejó de actualizar hace años es una noticia, aunque no
	// sea un error: notarum estaría sirviendo algo viejo sin saberlo.
	if !info.Modificado.IsZero() && time.Since(info.Modificado) > 2*365*24*time.Hour {
		t.Errorf("el catálogo no se actualiza desde %s", info.Modificado.Format("2006-01-02"))
	}
}

// El CSV sigue teniendo las columnas con las que se armó el lector, y las
// normas que trae siguen siendo reconocibles.
func TestRedElCatalogoSeSigueEntendiendo(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancelar()

	c := clienteReal(t)
	info, err := c.BuscarCatalogo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	destino := filepath.Join(t.TempDir(), "saij.csv")
	if err := c.DescargarCatalogo(ctx, info.URL, destino); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(destino)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	provincias := map[string]int{}
	tipos := map[string]int{}
	var sinTitulo, sinFecha int
	leidas, err := LeerCatalogo(f, func(n Norma) error {
		provincias[n.ProvinciaID]++
		tipos[n.Tipo]++
		if n.Titulo() == "" {
			sinTitulo++
		}
		if n.Anio() == 0 {
			sinFecha++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("normas: %d | provincias: %d | tipos: %d", leidas, len(provincias), len(tipos))

	// Los números de la última medición fueron 81.403 normas y 24
	// jurisdicciones. Que bajen mucho quiere decir que algo se rompió.
	if leidas < 70000 {
		t.Errorf("sólo se leyeron %d normas; venían siendo más de 81 mil", leidas)
	}
	if len(provincias) < 24 {
		t.Errorf("aparecen %d jurisdicciones y son 24", len(provincias))
	}
	if sinTitulo > 0 {
		t.Errorf("%d normas quedaron sin nada que mostrar", sinTitulo)
	}
	// Alguna fila sin fecha se perdona; que sean muchas es otra cosa.
	if sinFecha > leidas/100 {
		t.Errorf("%d normas sin fecha usable, de %d", sinFecha, leidas)
	}

	// Y las provincias que trae tienen que estar todas en la tabla.
	conocidas := map[string]bool{}
	for _, p := range Provincias {
		conocidas[p.ID] = true
	}
	for id := range provincias {
		if !conocidas[id] {
			t.Errorf("la base trae la jurisdicción %q, que no está en la tabla", id)
		}
	}
}
