package saij

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// El portal corre CKAN, así que la dirección del CSV se pregunta en vez de
// escribirse a mano: el archivo se vuelve a publicar con otro identificador
// cada tanto, y una URL fija se rompería sin avisar.
const (
	// BasePortal es datos.jus.gob.ar, el portal de datos del Ministerio de
	// Justicia.
	BasePortal = "https://datos.jus.gob.ar"
	// Dataset es el nombre del conjunto en el portal.
	Dataset = "base-saij-de-normativa-provincial"
)

// Opciones configura el cliente.
type Opciones struct {
	Base      string // para los tests
	UserAgent string
	HTTP      *http.Client
}

// Cliente habla con el portal.
type Cliente struct {
	base      string
	userAgent string
	http      *http.Client
}

func NuevoCliente(o Opciones) *Cliente {
	c := &Cliente{
		base:      strings.TrimRight(strings.TrimSpace(o.Base), "/"),
		userAgent: o.UserAgent,
		http:      o.HTTP,
	}
	if c.base == "" {
		c.base = BasePortal
	}
	if c.userAgent == "" {
		c.userAgent = "notarum (+https://github.com/diegoparras/notarum)"
	}
	if c.http == nil {
		// El CSV son 28 MB: el tiempo se le da a la descarga entera, no a
		// cada lectura.
		c.http = &http.Client{Timeout: 10 * time.Minute}
	}
	return c
}

// ErrDelPortal envuelve lo que sale mal del lado del portal, para poder
// distinguirlo de un error de notarum al informarlo.
type ErrDelPortal struct {
	Que   string
	Causa error
}

func (e *ErrDelPortal) Error() string {
	if e.Causa == nil {
		return "datos.jus.gob.ar: " + e.Que
	}
	return "datos.jus.gob.ar: " + e.Que + ": " + e.Causa.Error()
}

func (e *ErrDelPortal) Unwrap() error { return e.Causa }

// InfoCatalogo es lo que el portal dice del archivo publicado.
type InfoCatalogo struct {
	URL string
	// Modificado es cuándo se publicó esta versión. Sirve para no volver a
	// bajar 28 MB si no cambió nada.
	Modificado time.Time
	Formato    string
}

// BuscarCatalogo le pregunta al portal dónde está el CSV y de cuándo es.
func (c *Cliente) BuscarCatalogo(ctx context.Context) (*InfoCatalogo, error) {
	destino := c.base + "/api/3/action/package_show?id=" + url.QueryEscape(Dataset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, destino, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, &ErrDelPortal{Que: "no se pudo preguntar por el catálogo", Causa: err}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, &ErrDelPortal{Que: fmt.Sprintf("contestó %d al preguntar por el catálogo", res.StatusCode)}
	}

	var doc struct {
		Exito     bool `json:"success"`
		Resultado struct {
			Recursos []struct {
				Nombre       string `json:"name"`
				URL          string `json:"url"`
				Formato      string `json:"format"`
				UltimaMod    string `json:"last_modified"`
				MetadataMod  string `json:"metadata_modified"`
				DatastoreAct bool   `json:"datastore_active"`
			} `json:"resources"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&doc); err != nil {
		return nil, &ErrDelPortal{Que: "la respuesta del catálogo no se pudo leer", Causa: err}
	}
	if !doc.Exito {
		return nil, &ErrDelPortal{Que: "el portal dijo que el pedido no salió bien"}
	}

	// El conjunto trae también un tablero de gráficos; el que sirve es el CSV.
	for _, r := range doc.Resultado.Recursos {
		if !strings.EqualFold(strings.TrimSpace(r.Formato), "CSV") {
			continue
		}
		if r.URL == "" {
			continue
		}
		info := &InfoCatalogo{URL: r.URL, Formato: "CSV"}
		for _, cuando := range []string{r.UltimaMod, r.MetadataMod} {
			if t, err := parsearMomento(cuando); err == nil {
				info.Modificado = t
				break
			}
		}
		return info, nil
	}
	return nil, &ErrDelPortal{Que: "el conjunto ya no publica ningún CSV"}
}

// parsearMomento lee las fechas de CKAN, que vienen sin zona horaria y a veces
// sin fracción de segundo.
func parsearMomento(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("vacío")
	}
	for _, formato := range []string{
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		time.RFC3339,
	} {
		if t, err := time.Parse(formato, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("no se entiende la fecha %q", s)
}

// DescargarCatalogo baja el CSV a un archivo.
//
// Se baja a disco y no a memoria porque son 28 MB que después se leen en
// streaming: tenerlos dos veces no aporta nada.
func (c *Cliente) DescargarCatalogo(ctx context.Context, desde, destino string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, desde, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.userAgent)

	res, err := c.http.Do(req)
	if err != nil {
		return &ErrDelPortal{Que: "no se pudo bajar el catálogo", Causa: err}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &ErrDelPortal{Que: fmt.Sprintf("contestó %d al bajar el catálogo", res.StatusCode)}
	}

	// Se escribe en un archivo aparte y se renombra al final: si la descarga
	// se corta, no queda un catálogo a medias que parezca entero.
	tmp := destino + ".parcial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return &ErrDelPortal{Que: "se cortó la descarga del catálogo", Causa: err}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, destino)
}
