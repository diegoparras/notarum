package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/diegoparras/notarum/internal/cuentas"
)

// zona dice qué parte del servicio se está pidiendo. Cada una tiene su propia
// cuota, porque son usos distintos: una persona mirando el sitio hace muchos
// más pedidos que un programa bien escrito, y mezclarlas dejaba la interfaz
// inusable apenas se bajaba el límite de la API.
type zona int

const (
	zonaLector zona = iota
	zonaAPI
	zonaMCP
	zonaLogin
	// zonaPuerta es por dónde se entra: la pantalla de login y la vuelta del
	// proveedor de identidad. Se limita como el lector, pero no se cierra
	// nunca: para entrar haría falta haber entrado.
	zonaPuerta
	// zonaFeed son los feeds de alertas. Llevan su propia clave en la
	// dirección y la comprueba quien los atiende, así que no se cierran; se
	// limitan como el lector, porque un lector de feeds que se descontrola
	// pide mucho.
	zonaFeed
	zonaLibre // salud y archivos estáticos: no se limitan ni se cierran
)

func zonaDe(r *http.Request) zona {
	p := r.URL.Path
	switch {
	case p == "/v1/salud" || strings.HasPrefix(p, "/estatico/"):
		return zonaLibre
	case p == "/entrar" && r.Method == http.MethodPost:
		return zonaLogin
	case p == "/entrar" || p == "/salir" || strings.HasPrefix(p, "/entrar/"):
		// La puerta. Si cayera en la zona del lector, una instancia en modo
		// cerrado redirigiría /entrar a /entrar para siempre: es lo que pasó
		// en cuanto la primera cuenta hizo que el modo por defecto pasara a
		// cerrado.
		return zonaPuerta
	case strings.HasPrefix(p, "/feed/"):
		// El feed trae su propia clave en la dirección, y el que la atiende la
		// comprueba. Si cayera en la zona del lector, una instancia cerrada lo
		// mandaría a /entrar, y un lector de feeds no sabe entrar a ningún
		// lado: la función quedaría inservible justo donde más se usa.
		return zonaFeed
	case strings.HasPrefix(p, "/mcp"):
		return zonaMCP
	case strings.HasPrefix(p, "/v1/"):
		return zonaAPI
	}
	return zonaLector
}

// guardia aplica la política de la instancia: quién puede pedir qué y cuánto.
type guardia struct {
	reg *cuentas.Registro
	// politica es la de arranque, que rige cuando no hay registro donde
	// guardar los cambios.
	politica cuentas.Politica
	limite   *limitador
}

// vigente es la política que rige ahora. Se pregunta en cada pedido porque se
// puede cambiar desde el panel sin reiniciar.
func (g *guardia) vigente() cuentas.Politica {
	if g.reg != nil {
		return g.reg.Politica()
	}
	return g.politica
}

func nuevaGuardia(reg *cuentas.Registro, p cuentas.Politica) *guardia {
	return &guardia{reg: reg, politica: p, limite: nuevoLimitador(0)}
}

// quienEs identifica a quien hace el pedido: por token si trae uno, o por la
// cookie de sesión si viene del navegador.
//
// Devuelve un error sólo cuando el token vino y era inválido: eso hay que
// decirlo, no tratarlo como anónimo, porque quien lo usa tiene que enterarse.
func (g *guardia) quienEs(r *http.Request, z zona) (*cuentas.Usuario, error) {
	if g.reg == nil {
		return nil, nil
	}
	if valor := cuentas.TokenDeCabecera(r.Header.Get("Authorization")); valor != "" {
		alcance := cuentas.AlcanceAPI
		if z == zonaMCP {
			alcance = cuentas.AlcanceMCP
		}
		_, u, err := g.reg.VerificarToken(valor, alcance)
		if err != nil {
			return nil, err
		}
		return u, nil
	}
	// Sin token, la sesión del navegador.
	if c, err := r.Cookie(nombreCookieSesion); err == nil && c.Value != "" {
		if u, err := g.reg.LeerSesion(c.Value); err == nil {
			return u, nil
		}
	}
	return nil, nil
}

// nombreCookieSesion tiene que ser el mismo que usa el lector.
const nombreCookieSesion = "notarum_sesion"

func (g *guardia) envolver(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		z := zonaDe(r)
		if z == zonaLibre {
			siguiente.ServeHTTP(w, r)
			return
		}

		u, err := g.quienEs(r, z)
		if err != nil {
			g.rechazarToken(w, r, z, err)
			return
		}

		if !g.permite(u, z) {
			g.pedirIdentificacion(w, r, z)
			return
		}

		quien, cuota := g.cupoDe(r, u, z)
		ok, quedan := g.limite.permitirCon(quien, cuota)
		if cuota > 0 && z != zonaLector {
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cuota))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(quedan))
		}
		if !ok {
			g.rechazarPorCuota(w, r, z, cuota)
			return
		}
		siguiente.ServeHTTP(w, r)
	})
}

func (g *guardia) permite(u *cuentas.Usuario, z zona) bool {
	p := g.vigente()
	switch z {
	case zonaPuerta:
		return true // por acá se entra: no se puede pedir haber entrado
	case zonaFeed:
		return true // trae su propia clave, y la mira quien lo atiende
	case zonaAPI:
		return p.PermiteAPI(u)
	case zonaMCP:
		return p.PermiteMCP(u)
	case zonaLector:
		return p.PermiteLector(u)
	}
	return true // el login siempre se puede intentar
}

// cupoDe dice contra qué cubo se cuenta este pedido y cuánto le toca.
func (g *guardia) cupoDe(r *http.Request, u *cuentas.Usuario, z zona) (string, int) {
	ip := ipDe(r)
	p := g.vigente()
	switch z {
	case zonaLogin:
		// Los intentos de entrada se cuentan por dirección y aparte: es el
		// único límite pensado para frenar a alguien.
		return "login:" + ip, p.Login
	case zonaLector, zonaPuerta, zonaFeed:
		if u != nil {
			return "lector:" + u.Nombre, p.Lector
		}
		return "lector:" + ip, p.Lector
	}
	if u != nil {
		// Identificado: la cuota es suya y no la comparte con quien salga por
		// la misma dirección.
		return "api:" + u.Nombre, p.CuotaDe(u)
	}
	return "api:" + ip, p.Anonimo
}

func (g *guardia) rechazarToken(w http.ResponseWriter, r *http.Request, z zona, err error) {
	mensaje, detalle := "token inválido", "revisá el valor, o creá uno nuevo desde tu cuenta"
	if errors.Is(err, cuentas.ErrRevocado) {
		mensaje, detalle = "token revocado", "creá uno nuevo desde tu cuenta"
	}
	if z == zonaLector {
		http.Redirect(w, r, "/entrar", http.StatusFound)
		return
	}
	escribirError(w, r, http.StatusUnauthorized, OrigenPedido, mensaje, detalle)
}

func (g *guardia) pedirIdentificacion(w http.ResponseWriter, r *http.Request, z zona) {
	if z == zonaLector {
		http.Redirect(w, r, "/entrar", http.StatusFound)
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="notarum"`)
	escribirError(w, r, http.StatusUnauthorized, OrigenPedido,
		"esta instancia pide identificarse",
		"mandá un token en Authorization: Bearer. Se crean desde /cuenta.")
}

func (g *guardia) rechazarPorCuota(w http.ResponseWriter, r *http.Request, z zona, cuota int) {
	w.Header().Set("Retry-After", "60")
	if z == zonaLogin {
		escribirError(w, r, http.StatusTooManyRequests, OrigenPedido,
			"demasiados intentos de entrada",
			"esperá un minuto antes de volver a probar")
		return
	}
	escribirError(w, r, http.StatusTooManyRequests, OrigenPedido, "demasiados pedidos",
		"el límite es de "+strconv.Itoa(cuota)+" pedidos por minuto")
}
