package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// Handler expone el servidor MCP por HTTP: un POST con un mensaje JSON-RPC
// devuelve un JSON-RPC. Es el transporte que sirve para una instancia
// desplegada, donde no hay entrada estándar que compartir.
//
// Token, si no está vacío, exige "Authorization: Bearer <token>". Una
// instancia pública de sólo lectura puede dejarlo abierto; una que se quiera
// reservada, no.
func (s *Servidor) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodGet:
			// Un GET sirve para saber que el endpoint está y cómo hablarle.
			escribir(w, http.StatusOK, map[string]any{
				"servidor":       "notarum",
				"protocolo":      VersionProtocolo,
				"transporte":     "JSON-RPC 2.0 por POST",
				"metodos":        []string{"initialize", "tools/list", "tools/call", "ping"},
				"requiere_token": token != "",
			})
			return
		case http.MethodPost:
		default:
			w.Header().Set("Allow", "GET, POST, OPTIONS")
			escribir(w, http.StatusMethodNotAllowed, mapaError(CodigoPedidoInvalido, "usá POST"))
			return
		}

		if token != "" && !autorizado(r, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="notarum"`)
			escribir(w, http.StatusUnauthorized, mapaError(CodigoPedidoInvalido, "falta el token"))
			return
		}

		crudo, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			escribir(w, http.StatusBadRequest, mapaError(CodigoParseo, "no se pudo leer el pedido"))
			return
		}
		res := s.Atender(r.Context(), crudo)
		if res == nil {
			// Era una notificación: no lleva cuerpo.
			w.WriteHeader(http.StatusAccepted)
			return
		}
		escribir(w, http.StatusOK, res)
	})
}

func autorizado(r *http.Request, token string) bool {
	cabecera := strings.TrimSpace(r.Header.Get("Authorization"))
	dado := strings.TrimSpace(strings.TrimPrefix(cabecera, "Bearer"))
	// Comparación simple: el token es un secreto compartido, no una contraseña
	// de usuario, y el largo no es información aprovechable acá.
	return dado != "" && dado == token
}

func mapaError(codigo int, mensaje string) *Respuesta {
	return &Respuesta{JSONRPC: "2.0", Error: &ErrorRPC{Codigo: codigo, Mensaje: mensaje}}
}

func escribir(w http.ResponseWriter, codigo int, cuerpo any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(codigo)
	if err := json.NewEncoder(w).Encode(cuerpo); err != nil {
		slog.Error("no se pudo escribir la respuesta MCP", "err", err)
	}
}
