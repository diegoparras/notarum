package infoleg

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/diegoparras/notarum/internal/htmltexto"
)

// ErrSinTexto indica que InfoLEG no publicó el texto de esa norma. No es una
// falla: pasa con más de la mitad del catálogo, y el sitio lo dice mandando a
// una página de archivo inexistente.
var ErrSinTexto = errors.New("InfoLEG no publicó el texto de esta norma")

// ErrDelSitio envuelve lo que salió mal del lado de InfoLEG o del portal de
// datos, para poder distinguirlo de un problema propio.
type ErrDelSitio struct {
	Operacion string
	URL       string
	Codigo    int
	Causa     error
}

func (e *ErrDelSitio) Error() string {
	if e.Codigo != 0 {
		return fmt.Sprintf("%s: InfoLEG respondió %d en %s", e.Operacion, e.Codigo, e.URL)
	}
	return fmt.Sprintf("%s: %v", e.Operacion, e.Causa)
}

func (e *ErrDelSitio) Unwrap() error { return e.Causa }

// BaseDatos es el portal de datos abiertos donde se publica el catálogo.
const BaseDatos = "https://datos.jus.gob.ar"

// DatasetCatalogo es el identificador del catálogo en el portal.
const DatasetCatalogo = "base-de-datos-legislativos-infoleg"

// Opciones configura el cliente.
type Opciones struct {
	Base      string // origen de los textos; por defecto BaseSitio
	BaseDatos string // origen del catálogo; por defecto BaseDatos
	UserAgent string
	Intervalo time.Duration // espera mínima entre pedidos
	Timeout   time.Duration
	HTTP      *http.Client
}

// Cliente lee InfoLEG con el mismo cuidado que el resto de notarum lee el
// Boletín: de a un pedido por vez y sin apuro. Es un sitio público del Estado.
type Cliente struct {
	base      string
	baseDatos string
	ua        string
	intervalo time.Duration
	http      *http.Client

	mu      sync.Mutex
	proximo time.Time
}

func NuevoCliente(o Opciones) *Cliente {
	if o.Base == "" {
		o.Base = BaseSitio
	}
	if o.BaseDatos == "" {
		o.BaseDatos = BaseDatos
	}
	if o.UserAgent == "" {
		o.UserAgent = "notarum (+https://github.com/diegoparras/notarum)"
	}
	if o.Intervalo <= 0 {
		o.Intervalo = 500 * time.Millisecond
	}
	if o.Timeout <= 0 {
		// El catálogo pesa 50 MB: el timeout no puede ser el de una página.
		o.Timeout = 5 * time.Minute
	}
	cli := o.HTTP
	if cli == nil {
		cli = &http.Client{Timeout: o.Timeout}
	}
	// Un 302 significa "no existe el archivo": hay que verlo, no seguirlo.
	cli.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Cliente{
		base:      strings.TrimRight(o.Base, "/"),
		baseDatos: strings.TrimRight(o.BaseDatos, "/"),
		ua:        o.UserAgent,
		intervalo: o.Intervalo,
		http:      cli,
	}
}

func (c *Cliente) esperarTurno(ctx context.Context) error {
	c.mu.Lock()
	ahora := time.Now()
	espera := time.Duration(0)
	if ahora.Before(c.proximo) {
		espera = c.proximo.Sub(ahora)
	}
	c.proximo = ahora.Add(espera + c.intervalo)
	c.mu.Unlock()

	if espera <= 0 {
		return nil
	}
	t := time.NewTimer(espera)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Texto es una norma leída de InfoLEG.
type Texto struct {
	ID    int    `json:"id"`
	HTML  string `json:"html"`
	Texto string `json:"texto"`
	URL   string `json:"url"`
}

// TraerTexto baja el texto de una norma por su id.
//
// Devuelve ErrSinTexto cuando InfoLEG no publicó el archivo, que se reconoce
// porque redirige a mostrarArchivoInexistente.
func (c *Cliente) TraerTexto(ctx context.Context, id int) (*Texto, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id de norma inválido: %d", id)
	}
	destino := c.urlTexto(id)
	if err := c.esperarTurno(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, destino, nil)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "leer norma", URL: destino, Causa: err}
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept-Language", "es-AR,es;q=0.9")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "leer norma", URL: destino, Causa: err}
	}
	defer res.Body.Close()

	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return nil, ErrSinTexto
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrSinTexto
	}
	if res.StatusCode != http.StatusOK {
		return nil, &ErrDelSitio{Operacion: "leer norma", URL: destino, Codigo: res.StatusCode}
	}

	crudo, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "leer norma", URL: destino, Causa: err}
	}

	// InfoLEG entrega ISO-8859-1: sin convertir, cada acento sale roto.
	limpio := htmltexto.Sanear(htmltexto.DesdeLatin1(crudo))
	plano := htmltexto.APlano(limpio)
	if strings.TrimSpace(plano) == "" {
		return nil, ErrSinTexto
	}
	return &Texto{ID: id, HTML: limpio, Texto: plano, URL: destino}, nil
}

func (c *Cliente) urlTexto(id int) string {
	base := (id / TamañoCarpeta) * TamañoCarpeta
	return fmt.Sprintf("%s/anexos/%d-%d/%d/norma.htm", c.base, base, base+TamañoCarpeta-1, id)
}

// ------------------------------------------------------------------ catálogo

// InfoCatalogo describe la publicación del catálogo en el portal de datos.
type InfoCatalogo struct {
	URL         string    `json:"url"`
	Actualizado time.Time `json:"actualizado"`
	Bytes       int64     `json:"bytes,omitempty"`
	// Las bases complementarias, que traen qué modificó a cada norma y qué
	// modifica cada una. El catálogo principal sólo trae las cuentas.
	Modificadas    Recurso `json:"modificadas,omitzero"`
	Modificatorias Recurso `json:"modificatorias,omitzero"`
}

// Recurso es un archivo publicado en el portal.
type Recurso struct {
	URL   string `json:"url,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
}

// Hay dice si el portal publicó este archivo.
func (r Recurso) Hay() bool { return r.URL != "" }

// BuscarCatalogo pregunta al portal de datos dónde está el catálogo y cuándo
// se actualizó por última vez. La URL no se escribe a mano porque cambia con
// cada publicación.
func (c *Cliente) BuscarCatalogo(ctx context.Context) (*InfoCatalogo, error) {
	destino := c.baseDatos + "/api/3/action/package_show?id=" + DatasetCatalogo
	if err := c.esperarTurno(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, destino, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)

	res, err := c.http.Do(req)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "buscar el catálogo", URL: destino, Causa: err}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, &ErrDelSitio{Operacion: "buscar el catálogo", URL: destino, Codigo: res.StatusCode}
	}

	var respuesta struct {
		Success bool `json:"success"`
		Result  struct {
			MetadataModified string `json:"metadata_modified"`
			Resources        []struct {
				Nombre  string `json:"name"`
				Formato string `json:"format"`
				URL     string `json:"url"`
				Bytes   int64  `json:"size"`
			} `json:"resources"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&respuesta); err != nil {
		return nil, &ErrDelSitio{Operacion: "buscar el catálogo", URL: destino, Causa: err}
	}
	if !respuesta.Success {
		return nil, &ErrDelSitio{
			Operacion: "buscar el catálogo", URL: destino,
			Causa: errors.New("el portal de datos no reconoció el dataset"),
		}
	}

	// Los ZIP, no los CSV de muestra: el de la base completa y las dos
	// complementarias, que se distinguen por el nombre.
	var info InfoCatalogo
	for _, r := range respuesta.Result.Resources {
		if !strings.EqualFold(r.Formato, "ZIP") {
			continue
		}
		nombre := strings.ToLower(strings.TrimSpace(r.Nombre))
		switch {
		case !strings.Contains(nombre, "complementaria"):
			if info.URL == "" {
				info.URL, info.Bytes = r.URL, r.Bytes
			}
		// "modificatorias" contiene "modificadas" como subcadena si se compara
		// mal: se mira la palabra entera y la más larga primero.
		case strings.Contains(nombre, "modificatoria"):
			info.Modificatorias = Recurso{URL: r.URL, Bytes: r.Bytes}
		case strings.Contains(nombre, "modificada"):
			info.Modificadas = Recurso{URL: r.URL, Bytes: r.Bytes}
		}
	}
	if info.URL == "" {
		return nil, &ErrDelSitio{
			Operacion: "buscar el catálogo", URL: destino,
			Causa: errors.New("el dataset no trae el ZIP de la base completa: ¿cambió la publicación?"),
		}
	}
	if t, err := time.Parse("2006-01-02T15:04:05.999999", respuesta.Result.MetadataModified); err == nil {
		info.Actualizado = t
	}
	// Las complementarias pueden faltar sin que eso sea un error: notarum
	// sigue sirviendo el catálogo, sólo que sin las relaciones.
	return &info, nil
}

// DescargarCatalogo baja el ZIP a un archivo. Son unos 50 MB, así que va a
// disco y no a memoria; además zip.Reader necesita poder saltar.
func (c *Cliente) DescargarCatalogo(ctx context.Context, url, destino string) error {
	if err := c.esperarTurno(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.ua)

	res, err := c.http.Do(req)
	if err != nil {
		return &ErrDelSitio{Operacion: "bajar el catálogo", URL: url, Causa: err}
	}
	defer res.Body.Close()
	// Acá sí conviene seguir la redirección del portal.
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		if loc := res.Header.Get("Location"); loc != "" {
			return c.DescargarCatalogo(ctx, loc, destino)
		}
	}
	if res.StatusCode != http.StatusOK {
		return &ErrDelSitio{Operacion: "bajar el catálogo", URL: url, Codigo: res.StatusCode}
	}

	f, err := os.Create(destino)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, res.Body); err != nil {
		return &ErrDelSitio{Operacion: "bajar el catálogo", URL: url, Causa: err}
	}
	return nil
}

// AbrirCatalogo abre el CSV que viene adentro del ZIP descargado. Hay que
// cerrar lo que devuelve.
func AbrirCatalogo(rutaZip string) (io.ReadCloser, error) {
	z, err := zip.OpenReader(rutaZip)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir el catálogo descargado: %w", err)
	}
	for _, f := range z.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			continue
		}
		r, err := f.Open()
		if err != nil {
			z.Close()
			return nil, err
		}
		return &lectorZip{Reader: r, zip: z}, nil
	}
	z.Close()
	return nil, errors.New("el catálogo descargado no tiene ningún CSV adentro")
}

// lectorZip cierra el archivo y el ZIP que lo contiene.
type lectorZip struct {
	io.Reader
	zip *zip.ReadCloser
}

func (l *lectorZip) Close() error { return l.zip.Close() }
