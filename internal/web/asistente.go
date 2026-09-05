package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/asistente"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/tareas"
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

// tiempoMaximoDeGeneracion es lo más que se espera antes de darlo por perdido.
// Más que el timeout del cliente HTTP, para que gane ése y pueda explicar que
// el proveedor tardó; éste es la red por si algo se cuelga antes de llegar ahí.
const tiempoMaximoDeGeneracion = 40 * time.Second

// tareaDe es la clave con la que corre la generación de una persona. Va con el
// nombre adentro: cada uno tiene la suya y no se pisan entre cuentas.
func tareaDe(quien string) string { return "asistente:" + quien }

// generar atiende el formulario del asistente.
//
// Lanza la generación y contesta en el acto, sin esperar al proveedor.
//
// Esperándolo, el pedido HTTP quedaba colgado de un tercero: si tardaba de
// más, o no volvía, el navegador terminaba mostrando la página de error del
// proxy —"el servicio no responde"— cuando el servicio estaba perfecto y el
// que tardaba era el proveedor. Un error que notarum puede explicar lo tiene
// que mostrar notarum, en su propia interfaz, y para eso hay que contestar
// antes de saber el resultado.
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
	if s.tareas == nil {
		s.fallo(w, r, http.StatusServiceUnavailable, "No hay con qué generar",
			"Esta instancia se levantó sin el ejecutor de tareas.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}

	pedido := strings.TrimSpace(r.PostFormValue("pedido"))
	if pedido == "" {
		s.dibujarDocs(w, r, "Escribí qué consulta querés que arme.", http.StatusBadRequest)
		return
	}
	// Un techo: lo que entra acá va a un proveedor que cobra por token, y lo
	// paga quien carga la clave.
	if len(pedido) > 2000 {
		s.dibujarDocs(w, r, "El pedido es muy largo: contá en pocas líneas qué consulta necesitás.",
			http.StatusBadRequest)
		return
	}

	// La clave se busca acá y no adentro de la tarea: si falta, o no se puede
	// leer, es algo que se arregla en la cuenta y hay que decirlo ahora.
	clave, err := s.registro.ClaveIA(u.Nombre)
	switch {
	case errors.Is(err, cuentas.ErrSinClaveIA):
		s.dibujarDocs(w, r, "Para usar el asistente cargá tu clave de OpenRouter en tu cuenta.",
			http.StatusBadRequest)
		return
	case errors.Is(err, cuentas.ErrClaveIAIlegible):
		s.dibujarDocs(w, r, "Tu clave guardada no se pudo leer. Cargala de nuevo desde tu cuenta.",
			http.StatusBadRequest)
		return
	case err != nil:
		s.dibujarDocs(w, r, primeraMayuscula(err.Error())+".", http.StatusInternalServerError)
		return
	}

	// El modelo lo elige cada uno desde su cuenta; el formulario puede pisarlo
	// para una consulta suelta.
	modelo := strings.TrimSpace(r.PostFormValue("modelo"))
	if modelo == "" {
		modelo = s.registro.ModeloIA(u.Nombre)
	}
	// La dirección se saca del pedido, que es el único lugar donde está: la
	// tarea corre después, sin pedido del que sacarla.
	base := baseVisible(r)

	err = s.tareas.Lanzar(tareaDe(u.Nombre), u.Nombre,
		func(ctx context.Context, avisar func(string)) (string, error) {
			// Un techo propio, además del que tiene el cliente HTTP. Es lo que
			// garantiza que la pantalla no se quede en "armando" para
			// siempre: pase lo que pase del otro lado, esto termina y cuenta
			// qué pasó.
			ctx, cancelar := context.WithTimeout(ctx, tiempoMaximoDeGeneracion)
			defer cancelar()

			avisar("armando el contexto de esta instancia")
			instrucciones, err := asistente.Instrucciones(base)
			if err != nil {
				return "", errors.New("no se pudo armar el contexto del asistente")
			}
			avisar("pidiéndole la consulta al proveedor")
			slog.Info("generando consulta", "quien", u.Nombre, "modelo", modelo,
				"largo_del_pedido", len(pedido))

			g, err := s.asistente.Generar(ctx, clave, modelo, instrucciones, pedido)
			if err != nil {
				slog.Warn("no se pudo generar la consulta", "quien", u.Nombre, "err", err)
				return "", errors.New(explicarDelProveedor(err))
			}
			slog.Info("consulta generada", "quien", u.Nombre, "modelo", g.Modelo,
				"tokens_entrada", g.TokensEntrada, "tokens_salida", g.TokensSalida,
				"tardo", g.Tardo.Round(time.Millisecond))
			// Queda como último avance: la página lo muestra al pie del
			// resultado, y es lo que le dice a quien puso la clave qué gastó.
			avisar(fmt.Sprintf("lo armó %s · %d tokens de entrada y %d de salida",
				g.Modelo, g.TokensEntrada, g.TokensSalida))
			return g.Texto, nil
		})
	if errors.Is(err, tareas.ErrYaCorre) {
		// No es un error: ya se está generando lo anterior. Se lo lleva a
		// verlo en vez de retarlo.
		http.Redirect(w, r, "/docs#asistente", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.dibujarDocs(w, r, primeraMayuscula(err.Error())+".", http.StatusInternalServerError)
		return
	}
	// 303: después de un POST, el redirect obliga a un GET. Así recargar la
	// página no vuelve a pedirle una generación al proveedor.
	http.Redirect(w, r, "/docs#asistente", http.StatusSeeOther)
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
	case errors.Is(err, asistente.ErrModeloDesconocido):
		return "Ese modelo ya no existe en OpenRouter. Elegí otro en tu cuenta: " +
			"la lista sale de lo que el proveedor ofrece hoy."
	case errors.Is(err, asistente.ErrProveedorLento):
		return "OpenRouter tardó más de lo que se puede esperar. Probá de nuevo, " +
			"o pedí algo más corto."
	}
	return "No se pudo generar la consulta: " + err.Error()
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

// guardarModeloIA anota con cuál generar.
func (s *Sitio) guardarModeloIA(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}
	elegido := strings.TrimSpace(r.PostFormValue("modelo"))
	// Que sea uno de los que el proveedor ofrece de verdad: guardar cualquier
	// texto haría que el error apareciera recién al generar, y contado por el
	// proveedor en vez de acá.
	if elegido != "" && s.asistente != nil {
		clave, err := s.registro.ClaveIA(u.Nombre)
		if err != nil {
			s.dibujarCuenta(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusBadRequest)
			return
		}
		modelos, err := s.asistente.Modelos(r.Context(), clave)
		if err != nil {
			s.dibujarCuenta(w, r, u, "", explicarDelProveedor(err), http.StatusBadGateway)
			return
		}
		if !entreLosModelos(modelos, elegido) {
			s.dibujarCuenta(w, r, u, "", "Ese modelo no está entre los que ofrece tu clave.",
				http.StatusBadRequest)
			return
		}
	}
	if err := s.registro.GuardarModeloIA(u.Nombre, elegido); err != nil {
		s.dibujarCuenta(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusBadRequest)
		return
	}
	slog.Info("modelo elegido", "quien", u.Nombre, "modelo", elegido)
	http.Redirect(w, r, "/cuenta", http.StatusSeeOther)
}

func entreLosModelos(ms []asistente.Modelo, id string) bool {
	for _, m := range ms {
		if m.ID == id {
			return true
		}
	}
	return false
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
