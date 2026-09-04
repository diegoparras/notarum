package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

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
