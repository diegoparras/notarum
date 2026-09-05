package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/asistente"
	"github.com/diegoparras/notarum/internal/cuentas"
)

// El asistente que arma la consulta.
//
// La documentación dice qué rutas hay y qué parámetros toman, pero traducir
// eso al cliente HTTP de n8n, a un script de Python o a un nodo de un
// automatizador es un trabajo aparte. Acá se escribe lo que se quiere y sale
// la consulta armada.
//
// La clave del proveedor la pone cada persona desde su cuenta y paga lo suyo.
// notarum no tiene una propia: no hay razón para que quien levanta el
// servicio le pague la generación a todo el que pase.

// ConAsistente enciende el generador de consultas.
func (s *Sitio) ConAsistente(c *asistente.Cliente) *Sitio {
	s.asistente = c
	return s
}

// PuedeAsistir dice si esta instancia tiene con qué generar.
func (s *Sitio) PuedeAsistir() bool { return s.asistente != nil && s.registro != nil }

// generar atiende el formulario del asistente y devuelve la consulta armada.
func (s *Sitio) generar(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	if !s.PuedeAsistir() {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no tiene el asistente",
			"Se enciende cuando hay cuentas: cada persona carga su clave de OpenRouter desde su cuenta.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}

	pedido := strings.TrimSpace(r.PostFormValue("pedido"))
	if pedido == "" {
		s.dibujarDocs(w, r, "", "Escribí qué consulta querés que arme.", http.StatusBadRequest)
		return
	}
	// Un techo: lo que entra acá va a un proveedor que cobra por token, y lo
	// paga quien carga la clave.
	if len(pedido) > 2000 {
		s.dibujarDocs(w, r, "", "El pedido es muy largo: contá en pocas líneas qué consulta necesitás.",
			http.StatusBadRequest)
		return
	}

	clave, err := s.registro.ClaveIA(u.Nombre)
	switch {
	case errors.Is(err, cuentas.ErrSinClaveIA):
		s.dibujarDocs(w, r, "", "Para usar el asistente cargá tu clave de OpenRouter en tu cuenta.",
			http.StatusBadRequest)
		return
	case errors.Is(err, cuentas.ErrClaveIAIlegible):
		s.dibujarDocs(w, r, "", "Tu clave guardada no se pudo leer. Cargala de nuevo desde tu cuenta.",
			http.StatusBadRequest)
		return
	case err != nil:
		s.dibujarDocs(w, r, "", primeraMayuscula(err.Error())+".", http.StatusInternalServerError)
		return
	}

	// Se anota antes de salir a la red y no sólo al volver.
	//
	// Con una sola línea al final, un pedido que no llega a terminar no deja
	// ninguna huella: el registro queda igual que si nadie hubiera pedido
	// nada, y desde afuera se ve como un servicio caído. Con ésta se sabe al
	// menos que el pedido entró y hasta dónde llegó.
	slog.Info("generando consulta", "quien", u.Nombre, "largo_del_pedido", len(pedido))

	instrucciones, err := asistente.Instrucciones(baseVisible(r))
	if err != nil {
		s.dibujarDocs(w, r, "", "No se pudo armar el contexto del asistente.", http.StatusInternalServerError)
		return
	}

	slog.Info("pidiéndole al proveedor", "quien", u.Nombre,
		"largo_de_las_instrucciones", len(instrucciones))

	g, err := s.asistente.Generar(r.Context(), clave, r.PostFormValue("modelo"), instrucciones, pedido)
	if err != nil {
		// También al log: la persona ve una explicación, pero quien opera
		// necesita el error de verdad para saber si es del proveedor o propio.
		slog.Warn("no se pudo generar la consulta", "quien", u.Nombre, "err", err)
		s.dibujarDocs(w, r, "", explicarDelProveedor(err), http.StatusBadGateway)
		return
	}
	slog.Info("consulta generada", "quien", u.Nombre, "modelo", g.Modelo,
		"tokens_entrada", g.TokensEntrada, "tokens_salida", g.TokensSalida,
		"tardo", g.Tardo.Round(time.Millisecond))

	s.dibujarDocsCon(w, r, datosAsistente{
		Pedido: pedido, Generado: g.Texto, Modelo: g.Modelo,
		TokensEntrada: g.TokensEntrada, TokensSalida: g.TokensSalida,
	}, "", http.StatusOK)
}

// explicarDelProveedor traduce el error a algo que diga qué hacer.
func explicarDelProveedor(err error) string {
	switch {
	case errors.Is(err, asistente.ErrClaveRechazada):
		return "OpenRouter rechazó tu clave. Revisala y cargala de nuevo desde tu cuenta."
	case errors.Is(err, asistente.ErrSinSaldo):
		return "Tu cuenta de OpenRouter no tiene saldo."
	case errors.Is(err, asistente.ErrProveedorOcupado):
		return "OpenRouter está limitando los pedidos. Probá de nuevo en un rato."
	case errors.Is(err, asistente.ErrProveedorLento):
		return "OpenRouter tardó más de lo que se puede esperar. Probá de nuevo, " +
			"o pedí algo más corto."
	}
	return "No se pudo generar la consulta: " + err.Error()
}

// datosAsistente es lo que la página muestra del asistente.
type datosAsistente struct {
	Pedido        string
	Generado      string
	Modelo        string
	TokensEntrada int
	TokensSalida  int
}

// ------------------------------------------------------------- la clave

// guardarClaveIA atiende el campo de la cuenta.
func (s *Sitio) guardarClaveIA(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}
	clave := strings.TrimSpace(r.PostFormValue("clave_ia"))
	if clave == "" {
		s.dibujarCuenta(w, r, u, "", "Pegá la clave de OpenRouter.", http.StatusBadRequest)
		return
	}

	// Se prueba antes de guardarla: mejor enterarse acá que cuando alguien
	// quiere generar algo y no entiende por qué falla.
	if s.asistente != nil {
		if err := s.asistente.Probar(r.Context(), clave); err != nil {
			s.dibujarCuenta(w, r, u, "", explicarDelProveedor(err), http.StatusBadRequest)
			return
		}
	}
	if err := s.registro.GuardarClaveIA(u.Nombre, clave); err != nil {
		s.dibujarCuenta(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusBadRequest)
		return
	}
	slog.Info("clave de IA cargada", "quien", u.Nombre)
	http.Redirect(w, r, "/cuenta", http.StatusSeeOther)
}

// borrarClaveIA la saca: es de quien la cargó.
func (s *Sitio) borrarClaveIA(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	if err := s.registro.BorrarClaveIA(u.Nombre); err != nil {
		s.dibujarCuenta(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/cuenta", http.StatusSeeOther)
}
