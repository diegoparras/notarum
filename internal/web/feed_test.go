package web

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/alertas"
)

// El feed es la forma más barata de que algo avise: lo lee cualquier cosa y no
// hace falta tener un servidor escuchando, que es lo que un webhook sí pide.
func TestElFeedDeUnaAlerta(t *testing.T) {
	srv, reg := sitioConAlertas(t)
	a, err := reg.Crear(alertas.Alerta{
		Dueño: "diego", Nombre: "ENACOM", Fuente: alertas.FuenteNacional,
		Criterios: alertas.Criterios{Texto: "enacom"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.Ultimas = []alertas.Coincidencia{
		{ID: "nacional:7", Titulo: "Resolución 7", Detalle: "De qué trata", Fecha: "2026-09-01", Enlace: "/norma/7"},
	}
	clave, err := alertas.NuevaClaveFeed()
	if err != nil {
		t.Fatal(err)
	}
	a.ClaveFeed = clave
	if err := reg.Actualizar(a); err != nil {
		t.Fatal(err)
	}

	res, cuerpo := pedir(t, srv, "/feed/"+a.ID+"?k="+url.QueryEscape(clave))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("contestó %d: %s", res.StatusCode, recorte(cuerpo))
	}
	if tipo := res.Header.Get("Content-Type"); !strings.HasPrefix(tipo, "application/atom+xml") {
		t.Errorf("content-type = %q", tipo)
	}
	// Que sea XML de verdad: un feed roto lo descarta el lector entero y no
	// vuelve a intentarlo.
	var f struct {
		Titulo   string `xml:"title"`
		Entradas []struct {
			Titulo string `xml:"title"`
			ID     string `xml:"id"`
			Enlace struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal([]byte(cuerpo), &f); err != nil {
		t.Fatalf("el feed no es XML válido: %v", err)
	}
	if !strings.Contains(f.Titulo, "ENACOM") {
		t.Errorf("título = %q", f.Titulo)
	}
	if len(f.Entradas) != 1 || f.Entradas[0].Titulo != "Resolución 7" {
		t.Fatalf("entradas = %+v", f.Entradas)
	}
	// Los enlaces tienen que ser absolutos: un lector de feeds no sabe desde
	// dónde se bajó la lista.
	if !strings.HasPrefix(f.Entradas[0].Enlace.Href, "http") {
		t.Errorf("el enlace no es absoluto: %q", f.Entradas[0].Enlace.Href)
	}
}

// Sin la clave no hay feed, y una alerta que no existe contesta lo mismo:
// distinguirlas dejaría averiguar qué alertas hay probando.
func TestSinLaClaveNoHayFeed(t *testing.T) {
	srv, reg := sitioConAlertas(t)
	a, _ := reg.Crear(alertas.Alerta{
		Dueño: "diego", Nombre: "sin feed", Fuente: alertas.FuenteNacional,
		Criterios: alertas.Criterios{Texto: "x"},
	})
	for _, direccion := range []string{
		"/feed/" + a.ID,
		"/feed/" + a.ID + "?k=inventada",
		"/feed/inventada?k=inventada",
	} {
		res, _ := pedir(t, srv, direccion)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s contestó %d", direccion, res.StatusCode)
		}
	}
}

// Una instancia cerrada no puede mandar el feed a la pantalla de entrada: un
// lector de feeds no sabe entrar a ningún lado.
func TestElFeedAndaConLaInstanciaCerrada(t *testing.T) {
	srv, reg := sitioConAlertas(t)
	a, _ := reg.Crear(alertas.Alerta{
		Dueño: "diego", Nombre: "cerrada", Fuente: alertas.FuenteNacional,
		Criterios: alertas.Criterios{Texto: "x"},
	})
	clave, _ := alertas.NuevaClaveFeed()
	a.ClaveFeed = clave
	reg.Actualizar(a)

	res, cuerpo := pedir(t, srv, "/feed/"+a.ID+"?k="+url.QueryEscape(clave))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("contestó %d", res.StatusCode)
	}
	if strings.Contains(cuerpo, "<html") {
		t.Error("devolvió una página en vez del feed")
	}
}
