package web

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/alertas"
)

// El feed de una alerta.
//
// Es la forma más vieja y más barata de que algo avise: no hay que programar
// nada del otro lado, lo lee cualquier cosa —un lector de feeds, un nodo de
// n8n, el navegador— y no hace falta que quien recibe tenga un servidor
// escuchando, que es lo que un webhook sí necesita.
//
// Atom y no RSS: tiene fechas con huso horario, identificadores estables por
// entrada y está definido en un estándar en vez de en la costumbre. Todo lo
// que lee RSS lee Atom.

// feedAlerta devuelve las últimas coincidencias de una alerta.
func (s *Sitio) feedAlerta(w http.ResponseWriter, r *http.Request) {
	if !s.PuedeAlertar() {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no tiene alertas", "")
		return
	}
	a, hay := s.alertas.Leer(r.PathValue("id"))
	// La misma respuesta para una alerta que no existe y para una clave que no
	// abre: distinguirlas dejaría averiguar qué alertas hay probando.
	if !hay || !a.AbreElFeed(r.URL.Query().Get("k")) {
		s.fallo(w, r, http.StatusNotFound, "No hay un feed acá",
			"La dirección lleva una clave, y sin ella no hay nada que mostrar.")
		return
	}
	escribirAtom(w, r, feedDe(a, baseVisible(r)))
}

// ------------------------------------------------------------------- Atom

type feed struct {
	XMLName   xml.Name  `xml:"http://www.w3.org/2005/Atom feed"`
	Titulo    string    `xml:"title"`
	Subtitulo string    `xml:"subtitle,omitempty"`
	ID        string    `xml:"id"`
	Actualiz  string    `xml:"updated"`
	Enlaces   []enlace  `xml:"link"`
	Autor     autor     `xml:"author"`
	Entradas  []entrada `xml:"entry"`
}

type enlace struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
	Tipo string `xml:"type,attr,omitempty"`
}

type autor struct {
	Nombre string `xml:"name"`
}

type entrada struct {
	Titulo   string `xml:"title"`
	ID       string `xml:"id"`
	Actualiz string `xml:"updated"`
	Enlace   enlace `xml:"link"`
	Resumen  string `xml:"summary,omitempty"`
}

func feedDe(a *alertas.Alerta, base string) feed {
	actualizado := a.UltimoAviso
	if actualizado.IsZero() {
		actualizado = a.Creada
	}
	f := feed{
		Titulo:    "notarum · " + a.Nombre,
		Subtitulo: "Novedades en " + a.Fuente.Nombre() + ". " + criteriosEnTexto(a.Criterios),
		// El identificador del feed lleva la alerta pero no su clave: el id se
		// guarda en el lector y se comparte al copiar una entrada.
		ID:       "urn:notarum:alerta:" + a.ID,
		Actualiz: actualizado.UTC().Format(time.RFC3339),
		Autor:    autor{Nombre: "notarum"},
		Enlaces:  []enlace{{Href: base + "/cuenta", Rel: "alternate", Tipo: "text/html"}},
	}
	for _, c := range a.Ultimas {
		e := entrada{
			Titulo:   c.Titulo,
			ID:       "urn:notarum:" + c.ID,
			Actualiz: fechaDeEntrada(c.Fecha, actualizado),
			Enlace:   enlace{Href: base + c.Enlace, Rel: "alternate", Tipo: "text/html"},
			Resumen:  c.Detalle,
		}
		if e.Titulo == "" {
			e.Titulo = c.ID
		}
		f.Entradas = append(f.Entradas, e)
	}
	return f
}

// fechaDeEntrada usa la de la norma si se entiende, y si no la del aviso: una
// entrada sin fecha válida hace que algunos lectores descarten el feed entero.
func fechaDeEntrada(fecha string, sino time.Time) string {
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(fecha)); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return sino.UTC().Format(time.RFC3339)
}

func escribirAtom(w http.ResponseWriter, r *http.Request, f feed) {
	// Se arma en memoria antes de mandar, igual que las páginas: si el XML
	// falla a mitad, sale un documento cortado que el lector no puede leer.
	crudo, err := xml.MarshalIndent(f, "", "  ")
	if err != nil {
		s := &Sitio{}
		_ = s
		http.Error(w, "no se pudo armar el feed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	// Que no se guarde en caches ajenas: la dirección lleva una clave.
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xml.Header))
	w.Write(crudo)
}
