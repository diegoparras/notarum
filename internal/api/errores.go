package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
)

// Origen dice de quién es la culpa de un error. Quien consume la API tiene que
// poder saber a quién mirar.
type Origen string

const (
	// OrigenSitio: el Boletín Oficial no contestó, contestó mal o cambió de forma.
	OrigenSitio Origen = "sitio"
	// OrigenNotarum: falló algo propio.
	OrigenNotarum Origen = "notarum"
	// OrigenPedido: el pedido está mal armado.
	OrigenPedido Origen = "pedido"
)

// RespuestaError es el cuerpo de todo error de la API.
type RespuestaError struct {
	Error      string `json:"error"`
	Detalle    string `json:"detalle,omitempty"`
	Origen     Origen `json:"origen"`
	SinEdicion bool   `json:"sin_edicion,omitempty"`
	// LimitadoHasta acompaña al 503 del límite del Boletín: a partir de
	// cuándo tiene sentido volver a pedir lo que no esté guardado. Es lo mismo
	// que la cabecera Retry-After, como hora.
	LimitadoHasta *time.Time `json:"limitado_hasta,omitempty"`
}

func escribirJSON(w http.ResponseWriter, r *http.Request, codigo int, cuerpo any, cacheControl string) {
	datos, err := json.Marshal(cuerpo)
	if err != nil {
		slog.Error("no se pudo serializar la respuesta", "err", err, "ruta", r.URL.Path)
		http.Error(w, `{"error":"no se pudo serializar la respuesta","origen":"notarum"}`, http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if cacheControl != "" {
		h.Set("Cache-Control", cacheControl)
	}

	suma := sha256.Sum256(datos)
	etag := `"` + hex.EncodeToString(suma[:16]) + `"`
	h.Set("ETag", etag)
	if coincide(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.WriteHeader(codigo)
	if r.Method != http.MethodHead {
		_, _ = w.Write(datos)
	}
}

func coincide(cabecera, etag string) bool {
	for _, parte := range strings.Split(cabecera, ",") {
		parte = strings.TrimSpace(parte)
		if parte == "*" || parte == etag || strings.TrimPrefix(parte, "W/") == etag {
			return true
		}
	}
	return false
}

func escribirError(w http.ResponseWriter, r *http.Request, codigo int, origen Origen, mensaje, detalle string) {
	escribirJSON(w, r, codigo, RespuestaError{Error: mensaje, Detalle: detalle, Origen: origen}, "no-store")
}

// escribirErrorDeLectura traduce un error de la capa de lectura a HTTP,
// distinguiendo lo que es culpa del sitio de lo que es nuestro.
func escribirErrorDeLectura(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, boletin.ErrSinEdicion):
		escribirJSON(w, r, http.StatusNotFound, RespuestaError{
			Error:      "no hubo edición ese día",
			Origen:     OrigenSitio,
			SinEdicion: true,
		}, "public, max-age=3600")
		return
	case errors.Is(err, r.Context().Err()) && r.Context().Err() != nil:
		escribirError(w, r, http.StatusRequestTimeout, OrigenNotarum,
			"el pedido se canceló antes de terminar", err.Error())
		return
	}

	var es *boletin.ErrDelSitio
	if errors.As(err, &es) && es.Limitado() {
		// El Boletín limita los pedidos por dirección y esta instancia llegó
		// al tope. No es un fallo del sitio ni del pedido: es cuestión de
		// esperar, y se dice cuánto.
		segundos := int(math.Ceil(es.ReintentarEn.Seconds()))
		if segundos < 1 {
			segundos = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(segundos))
		slog.Warn("el Boletín está limitando los pedidos", "reintentar_en", segundos, "ruta", r.URL.Path)
		// El motivo va adelante y con estas palabras. Cuando notarum pasaba
		// el 429 del sitio adentro de un 502, el detalle decía "el Boletín
		// Oficial respondió 429", y hay clientes que reconocen el caso por
		// ese texto para dejar de pedir. Sólo se dice cuando es cierto: si
		// frenó el tope propio, el sitio no respondió nada.
		motivo := "notarum llegó a su propio tope de pedidos al Boletín, que se adelanta al límite del sitio"
		if es.PorElSitio {
			motivo = "el Boletín Oficial respondió 429 (demasiados pedidos) y por un rato no se le pide nada"
		}
		hasta := time.Now().Add(es.ReintentarEn).UTC()
		escribirJSON(w, r, http.StatusServiceUnavailable, RespuestaError{
			Error:         "el Boletín Oficial está limitando los pedidos de esta instancia",
			Detalle:       motivo + "; probá de nuevo en " + strconv.Itoa(segundos) + " segundos; lo que ya está guardado se sigue sirviendo",
			Origen:        OrigenSitio,
			LimitadoHasta: &hasta,
		}, "no-store")
		return
	}
	if errors.As(err, &es) {
		codigo := http.StatusBadGateway
		if es.Codigo == http.StatusNotFound {
			codigo = http.StatusNotFound
		}
		slog.Warn("el sitio del Boletín falló", "err", err, "ruta", r.URL.Path)
		escribirError(w, r, codigo, OrigenSitio,
			"el Boletín Oficial no devolvió lo esperado", es.Error())
		return
	}

	slog.Error("error interno", "err", err, "ruta", r.URL.Path)
	escribirError(w, r, http.StatusInternalServerError, OrigenNotarum,
		"no se pudo completar el pedido", err.Error())
}
