package servicio

import (
	"archive/zip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/infoleg"
)

// catalogoDePrueba arma el ZIP que publica el portal de datos, con unas pocas
// normas reales.
func catalogoDePrueba(t *testing.T) []byte {
	t.Helper()
	csv := "id_norma,tipo_norma,numero_norma,fecha_boletin,titulo_resumido,texto_original,texto_actualizado,modificada_por,modifica_a\n" +
		// Con texto publicado.
		"401266,Ley,27742,2024-07-08,LEY DE BASES,http://x/norma.htm,,3,0\n" +
		// Sin texto: InfoLEG nunca lo publicó.
		"429014,Decreto,759,2026-08-20,RECURSO - RECHAZASE,,,0,0\n" +
		"429015,Decreto,762,2026-08-20,RECURSO - RECHAZASE,,,0,0\n" +
		// Con acento en el tipo, para probar el cruce.
		"374675,Resolución,15,2022-11-11,GATUZO,http://x/norma.htm,,0,0\n"

	var sb strings.Builder
	z := zip.NewWriter(&escritor{&sb})
	f, err := z.Create("base-infoleg-normativa-nacional.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(csv)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(sb.String())
}

type escritor struct{ sb *strings.Builder }

func (e *escritor) Write(p []byte) (int, error) { return e.sb.Write(p) }

// servicioConInfoLEG arma un servicio con InfoLEG apuntando a un sitio falso.
func servicioConInfoLEG(t *testing.T, textoNorma http.HandlerFunc) *Servicio {
	t.Helper()
	catalogo := catalogoDePrueba(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "package_show"):
			w.Write([]byte(`{"success":true,"result":{"metadata_modified":"2026-09-01T14:00:03.159085",
				"resources":[{"name":"Base Infoleg Normativa Nacional","format":"ZIP","url":"` +
				"http://" + r.Host + `/base.zip"}]}}`))
		case r.URL.Path == "/base.zip":
			w.Write(catalogo)
		case strings.Contains(r.URL.Path, "/anexos/"):
			if textoNorma != nil {
				textoNorma(w, r)
				return
			}
			w.Write([]byte("<html><body><p>Texto de la norma</p></body></html>"))
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConInfoLEG(infoleg.NuevoCliente(infoleg.Opciones{
			Base: srv.URL, BaseDatos: srv.URL, Intervalo: time.Millisecond,
		}))
}

func TestSincronizarInfoLEG(t *testing.T) {
	s := servicioConInfoLEG(t, nil)

	e, err := s.SincronizarInfoLEG(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Sincronizado || e.Normas != 4 {
		t.Errorf("estado = %+v", e)
	}
	if e.ConTexto != 2 {
		t.Errorf("con_texto = %d, se esperaban 2", e.ConTexto)
	}
	// La última fecha del catálogo es lo que dice cuánto atrasa InfoLEG.
	if e.UltimaFechaBO != "2026-08-20" {
		t.Errorf("ultima_fecha_boletin = %q", e.UltimaFechaBO)
	}
	// Y queda guardado para la próxima.
	if guardado := s.EstadoInfoLEG(); !guardado.Sincronizado || guardado.Normas != 4 {
		t.Errorf("estado guardado = %+v", guardado)
	}
}

// El cruce entre el aviso del Boletín y la norma del catálogo es lo que hace
// todo esto útil.
func TestNormaDelAviso(t *testing.T) {
	s := servicioConInfoLEG(t, nil)
	if _, err := s.SincronizarInfoLEG(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	fecha, _ := boletin.ParseFecha("2026-08-20")

	// Un decreto que está en el catálogo, tal como lo nombra el Boletín.
	n := s.NormaDelAviso(boletin.Aviso{Norma: "Decreto 759/2026", Fecha: fecha})
	if n == nil {
		t.Fatal("no se encontró el Decreto 759/2026")
	}
	if n.ID != 429014 {
		t.Errorf("id = %d", n.ID)
	}
	if n.TituloResumido != "RECURSO - RECHAZASE" {
		t.Errorf("titulo = %q", n.TituloResumido)
	}
	// InfoLEG no publicó su texto: no puede ofrecer un enlace roto.
	if n.TieneTexto || n.URLTexto() != "" {
		t.Errorf("el decreto figura con texto y no lo tiene: %+v", n)
	}
	if n.URLFicha() == "" {
		t.Error("la ficha tiene que existir igual")
	}
}

// El tipo con acento del catálogo tiene que cruzar con el que escribe el
// Boletín, con o sin tilde.
func TestNormaDelAvisoConAcentos(t *testing.T) {
	s := servicioConInfoLEG(t, nil)
	if _, err := s.SincronizarInfoLEG(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	fecha, _ := boletin.ParseFecha("2022-11-11")
	for _, escrito := range []string{"Resolución 15/2022", "Resolucion 15/2022", "RESOLUCIÓN 15/2022"} {
		n := s.NormaDelAviso(boletin.Aviso{Norma: escrito, Fecha: fecha})
		if n == nil {
			t.Errorf("no cruzó %q", escrito)
			continue
		}
		if n.ID != 374675 {
			t.Errorf("%q -> id = %d", escrito, n.ID)
		}
	}
}

// Un aviso que no nombra una norma —la segunda y la tercera sección— no puede
// devolver cualquier cosa.
func TestNormaDelAvisoSinNorma(t *testing.T) {
	s := servicioConInfoLEG(t, nil)
	if _, err := s.SincronizarInfoLEG(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	fecha, _ := boletin.ParseFecha("2026-08-20")
	for _, aviso := range []boletin.Aviso{
		{Norma: "", Fecha: fecha},
		{Norma: "PARTIDO FRENTE GRANDE", Fecha: fecha},
		{Norma: "Decreto 99999/2026", Fecha: fecha}, // no está en el catálogo
	} {
		if n := s.NormaDelAviso(aviso); n != nil {
			t.Errorf("%q devolvió %+v", aviso.Norma, n)
		}
	}
}

// Sin sincronizar el catálogo, el cruce no encuentra nada pero no rompe.
func TestNormaDelAvisoSinCatalogo(t *testing.T) {
	s := servicioConInfoLEG(t, nil)
	fecha, _ := boletin.ParseFecha("2026-08-20")
	if n := s.NormaDelAviso(boletin.Aviso{Norma: "Decreto 759/2026", Fecha: fecha}); n != nil {
		t.Errorf("sin catálogo devolvió %+v", n)
	}
	if e := s.EstadoInfoLEG(); e.Sincronizado {
		t.Error("dice estar sincronizado sin haberlo hecho")
	}
}

func TestTextoDeNormaSeCachea(t *testing.T) {
	var pedidosTexto int
	s := servicioConInfoLEG(t, func(w http.ResponseWriter, r *http.Request) {
		pedidosTexto++
		w.Write([]byte("<html><body><p>VISTO el expediente</p></body></html>"))
	})
	for i := 0; i < 3; i++ {
		texto, err := s.TextoDeNorma(context.Background(), 401266)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(texto.Texto, "VISTO el expediente") {
			t.Errorf("texto = %q", texto.Texto)
		}
	}
	if pedidosTexto != 1 {
		t.Errorf("se le pidió %d veces a InfoLEG: tendría que ser 1", pedidosTexto)
	}
}

// Que una norma no tenga texto también se guarda: no tiene sentido volver a
// preguntar por algo que InfoLEG nunca publicó.
func TestTextoDeNormaSinTextoSeCachea(t *testing.T) {
	var pedidosTexto int
	s := servicioConInfoLEG(t, func(w http.ResponseWriter, r *http.Request) {
		pedidosTexto++
		http.Redirect(w, r, "/infolegInternet/mostrarArchivoInexistente.do", http.StatusFound)
	})
	for i := 0; i < 3; i++ {
		if _, err := s.TextoDeNorma(context.Background(), 429014); !errors.Is(err, infoleg.ErrSinTexto) {
			t.Fatalf("err = %v", err)
		}
	}
	if pedidosTexto != 1 {
		t.Errorf("se le pidió %d veces a InfoLEG: tendría que ser 1", pedidosTexto)
	}
}

// Sin InfoLEG configurado, notarum sirve el Boletín igual: es un accesorio.
func TestSinInfoLEGTodoSigueAndando(t *testing.T) {
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm)

	if s.InfoLEGDisponible() {
		t.Error("dice tener InfoLEG sin haberlo configurado")
	}
	fecha, _ := boletin.ParseFecha("2026-08-20")
	if n := s.NormaDelAviso(boletin.Aviso{Norma: "Decreto 759/2026", Fecha: fecha}); n != nil {
		t.Errorf("devolvió %+v", n)
	}
	if _, err := s.TextoDeNorma(context.Background(), 1); err == nil {
		t.Error("se esperaba un error al pedir texto sin InfoLEG")
	}
	if _, err := s.SincronizarInfoLEG(context.Background(), t.TempDir(), nil); err == nil {
		t.Error("se esperaba un error al sincronizar sin InfoLEG")
	}
}

// Sincronizar 428 mil normas puede llevar rato: tiene que poder cortarse.
func TestSincronizarSeCorta(t *testing.T) {
	s := servicioConInfoLEG(t, nil)
	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()
	if _, err := s.SincronizarInfoLEG(ctx, t.TempDir(), nil); err == nil {
		t.Error("se esperaba que respetara la cancelación")
	}
}

// El ZIP descargado no puede quedar ocupando disco.
func TestSincronizarLimpiaElDescargado(t *testing.T) {
	s := servicioConInfoLEG(t, nil)
	dir := t.TempDir()
	if _, err := s.SincronizarInfoLEG(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	entradas, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entradas {
		if strings.HasSuffix(e.Name(), ".zip") {
			t.Errorf("quedó el descargado: %s", e.Name())
		}
	}
}
