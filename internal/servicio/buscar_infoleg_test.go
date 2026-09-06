package servicio

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/infoleg"
)

// El catálogo se guarda como lo que es: un ZIP, no JSON.
//
// El almacén envuelve lo que guarda en un sobre JSON, así que pasarle el ZIP en
// crudo falla con «invalid character 'P'». Y como ese error se anotaba y se
// seguía, el buscador de normativa nacional no se armaba nunca: quedaba
// diciendo que el catálogo no se bajó aunque acabara de bajarse. Encender
// NOTARUM_BUSCADOR_INFOLEG no servía de nada.
func TestElCatalogoGuardadoSobreviveAlReinicio(t *testing.T) {
	dir := t.TempDir()
	alm, err := almacen.NuevoDisco(dir)
	if err != nil {
		t.Fatal(err)
	}
	catalogo := catalogoDePrueba(t)
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "package_show") {
			w.Write([]byte(`{"success":true,"result":{"metadata_modified":"2026-09-01T14:00:03.159085",
				"resources":[{"name":"Base Infoleg Normativa Nacional","format":"ZIP","url":"` +
				"http://" + r.Host + `/base.zip"}]}}`))
			return
		}
		w.Write(catalogo)
	}))
	defer portal.Close()

	srv := Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConInfoLEG(infoleg.NuevoCliente(infoleg.Opciones{
			BaseDatos: portal.URL, Intervalo: time.Nanosecond,
		})).
		ConBuscadorInfoLEG(true)
	if _, err := srv.SincronizarInfoLEG(t.Context(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if !srv.CatalogoNacionalCargado() {
		t.Fatal("el buscador no quedó armado después de sincronizar")
	}
	alm.Cerrar()

	// Otro proceso, el mismo almacén: es lo que pasa al reiniciar. El buscador
	// tiene que armarse con lo guardado, sin volver a bajar nada.
	otro, err := almacen.NuevoDisco(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer otro.Cerrar()
	despues := Nuevo(boletin.NuevoCliente(boletin.Opciones{}), otro).
		ConInfoLEG(infoleg.NuevoCliente(infoleg.Opciones{BaseDatos: "http://no-se-usa"})).
		ConBuscadorInfoLEG(true)

	if !despues.CatalogoNacionalCargado() {
		t.Error("al reiniciar, el catálogo guardado no se pudo usar")
	}
}
