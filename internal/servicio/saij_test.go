package servicio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/saij"
)

func csvProvincial(t *testing.T) string {
	t.Helper()
	crudo, err := os.ReadFile(filepath.Join("..", "saij", "testdata", "normativa_provincial.csv"))
	if err != nil {
		t.Fatal(err)
	}
	return string(crudo)
}

// portalProvincial imita a datos.jus.gob.ar.
func portalProvincial(t *testing.T, csv string, publicado string) (*httptest.Server, *int) {
	t.Helper()
	var bajadas int
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/3/action/package_show", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"result":{"resources":[{"format":"CSV","url":"` +
			srv.URL + `/descarga.csv","last_modified":"` + publicado + `"}]}}`))
	})
	mux.HandleFunc("/descarga.csv", func(w http.ResponseWriter, r *http.Request) {
		bajadas++
		w.Write([]byte(csv))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &bajadas
}

func servicioConSAIJ(t *testing.T, srv *httptest.Server) *Servicio {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm)
	return s.ConSAIJ(saij.NuevoCliente(saij.Opciones{Base: srv.URL}))
}

func TestSincronizarSAIJ(t *testing.T) {
	srv, _ := portalProvincial(t, csvProvincial(t), "2026-09-01T10:57:16.703535")
	s := servicioConSAIJ(t, srv)

	if !s.SAIJDisponible() {
		t.Fatal("dice no tener la base provincial")
	}
	if s.EstadoSAIJ().Sincronizado {
		t.Error("dice estar sincronizado antes de sincronizar")
	}

	e, err := s.SincronizarSAIJ(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !e.Sincronizado || e.Normas != 48 {
		t.Fatalf("estado = %+v", e)
	}
	if e.Provincias < 10 {
		t.Errorf("provincias = %d", e.Provincias)
	}
	if e.CatalogoAlDia.IsZero() || e.SincronizadoEn.IsZero() {
		t.Error("el estado no dice cuándo")
	}
	// Y quedó guardado, para el próximo arranque.
	if !s.EstadoSAIJ().Sincronizado {
		t.Error("el estado no se guardó")
	}
}

// Si el portal publica lo mismo que ya está guardado, no hay por qué bajar
// 28 MB de nuevo.
func TestNoSeVuelveABajarLoMismo(t *testing.T) {
	srv, bajadas := portalProvincial(t, csvProvincial(t), "2026-09-01T10:57:16.703535")
	s := servicioConSAIJ(t, srv)

	if _, err := s.SincronizarSAIJ(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if *bajadas != 1 {
		t.Fatalf("bajadas = %d", *bajadas)
	}
	if _, err := s.SincronizarSAIJ(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if *bajadas != 1 {
		t.Errorf("se volvió a bajar el mismo catálogo: %d descargas", *bajadas)
	}
}

// Un catálogo ilegible no puede reemplazar al que ya andaba.
func TestUnCatalogoRotoNoPisaAlBueno(t *testing.T) {
	bueno := csvProvincial(t)
	srv, _ := portalProvincial(t, bueno, "2026-09-01T10:57:16.703535")
	s := servicioConSAIJ(t, srv)
	if _, err := s.SincronizarSAIJ(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	antes := s.BuscarProvincial(saij.Consulta{}).Total
	if antes == 0 {
		t.Fatal("no quedó nada del primer catálogo")
	}

	// Ahora el portal publica otra cosa, con otra fecha para que no lo saltee.
	roto, _ := portalProvincial(t, "una,cosa,rara\n1,2,3\n", "2026-09-02T10:00:00")
	s2 := s.ConSAIJ(saij.NuevoCliente(saij.Opciones{Base: roto.URL}))
	if _, err := s2.SincronizarSAIJ(context.Background(), t.TempDir()); err == nil {
		t.Fatal("se aceptó un catálogo que no se entiende")
	}
	if despues := s.BuscarProvincial(saij.Consulta{}).Total; despues != antes {
		t.Errorf("el catálogo bueno se perdió: %d -> %d", antes, despues)
	}
}

// El catálogo guardado se levanta al arrancar de nuevo, sin volver a bajarlo.
func TestElCatalogoSobreviveAlReinicio(t *testing.T) {
	dir := t.TempDir()
	srv, bajadas := portalProvincial(t, csvProvincial(t), "2026-09-01T10:57:16.703535")

	alm, err := almacen.NuevoDisco(dir)
	if err != nil {
		t.Fatal(err)
	}
	uno := Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConSAIJ(saij.NuevoCliente(saij.Opciones{Base: srv.URL}))
	if _, err := uno.SincronizarSAIJ(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	total := uno.BuscarProvincial(saij.Consulta{}).Total

	// Otro servicio sobre el mismo directorio: es lo que pasa al reiniciar.
	alm2, err := almacen.NuevoDisco(dir)
	if err != nil {
		t.Fatal(err)
	}
	dos := Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm2)
	if got := dos.BuscarProvincial(saij.Consulta{}).Total; got != total {
		t.Errorf("después de reiniciar hay %d normas y antes %d", got, total)
	}
	if *bajadas != 1 {
		t.Errorf("se bajó el catálogo %d veces", *bajadas)
	}
}

// Sin sincronizar, consultar no puede explotar: devuelve vacío y ya.
func TestConsultarSinCatalogo(t *testing.T) {
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm)

	if s.SAIJDisponible() {
		t.Error("dice tener la base provincial sin haberla configurado")
	}
	if s.CatalogoProvincialCargado() {
		t.Error("dice tener el catálogo cargado")
	}
	if r := s.BuscarProvincial(saij.Consulta{Texto: "algo"}); r.Total != 0 {
		t.Errorf("devolvió %d", r.Total)
	}
	if _, hay := s.NormaProvincial("LPB1000000"); hay {
		t.Error("encontró una norma sin catálogo")
	}
	// Las provincias se listan igual: son una constante, no dependen del
	// catálogo.
	if len(s.ProvinciasConNormas()) != 24 {
		t.Errorf("provincias = %d", len(s.ProvinciasConNormas()))
	}
	if _, err := s.SincronizarSAIJ(context.Background(), t.TempDir()); err == nil {
		t.Error("sincronizó sin tener la base configurada")
	}
}

// El índice se arma una sola vez aunque lo pidan muchos a la vez.
func TestElIndiceSeArmaUnaSolaVez(t *testing.T) {
	srv, _ := portalProvincial(t, csvProvincial(t), "2026-09-01T10:57:16.703535")
	s := servicioConSAIJ(t, srv)
	if _, err := s.SincronizarSAIJ(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}

	var espera sync.WaitGroup
	resultados := make([]int, 20)
	for k := range resultados {
		espera.Add(1)
		go func(k int) {
			defer espera.Done()
			resultados[k] = s.BuscarProvincial(saij.Consulta{Texto: "constitución"}).Total
		}(k)
	}
	espera.Wait()
	for k, r := range resultados {
		if r != resultados[0] {
			t.Fatalf("la consulta %d dio %d y la primera %d", k, r, resultados[0])
		}
	}
}

// Sincronizar mientras alguien consulta no puede dejar el índice a medias.
func TestSincronizarMientrasSeConsulta(t *testing.T) {
	srv, _ := portalProvincial(t, csvProvincial(t), "2026-09-01T10:57:16.703535")
	s := servicioConSAIJ(t, srv)
	if _, err := s.SincronizarSAIJ(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	esperado := s.BuscarProvincial(saij.Consulta{}).Total

	var espera sync.WaitGroup
	espera.Add(2)
	go func() {
		defer espera.Done()
		for k := 0; k < 20; k++ {
			otro, _ := portalProvincial(t, csvProvincial(t), "2026-09-0"+string(rune('1'+k%8))+"T10:00:00")
			s.ConSAIJ(saij.NuevoCliente(saij.Opciones{Base: otro.URL}))
			s.SincronizarSAIJ(context.Background(), t.TempDir())
		}
	}()
	go func() {
		defer espera.Done()
		for k := 0; k < 200; k++ {
			if got := s.BuscarProvincial(saij.Consulta{}).Total; got != esperado {
				t.Errorf("durante una sincronización se vio %d normas y son %d", got, esperado)
				return
			}
		}
	}()
	espera.Wait()
}

func TestNormaProvincialPorID(t *testing.T) {
	srv, _ := portalProvincial(t, csvProvincial(t), "2026-09-01T10:57:16.703535")
	s := servicioConSAIJ(t, srv)
	if _, err := s.SincronizarSAIJ(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	alguna := s.BuscarProvincial(saij.Consulta{Limite: 1}).Normas[0]

	n, hay := s.NormaProvincial(alguna.ID)
	if !hay {
		t.Fatalf("no se encontró %s", alguna.ID)
	}
	if n.URLFicha() == "" || !strings.Contains(n.URLFicha(), n.ID) {
		t.Errorf("la ficha no enlaza a la norma: %q", n.URLFicha())
	}
}

func TestTiposYProvincias(t *testing.T) {
	srv, _ := portalProvincial(t, csvProvincial(t), "2026-09-01T10:57:16.703535")
	s := servicioConSAIJ(t, srv)
	if _, err := s.SincronizarSAIJ(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}

	provincias := s.ProvinciasConNormas()
	if len(provincias) != 24 {
		t.Errorf("provincias = %d, son 24", len(provincias))
	}
	var conNormas int
	for _, p := range provincias {
		if p.Normas > 0 {
			conNormas++
		}
		if p.Nombre == "" || p.ID == "" {
			t.Errorf("provincia incompleta: %+v", p)
		}
	}
	if conNormas == 0 {
		t.Error("ninguna provincia tiene normas")
	}
	if len(s.TiposProvinciales()) == 0 {
		t.Error("no hay tipos de norma")
	}
}
