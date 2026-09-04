package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/diegoparras/notarum/internal/cuentas"
)

// claveContexto evita que otra cosa pise lo que se guarda en el contexto.
type claveContexto int

const (
	claveUsuario claveContexto = iota
	claveToken
)

func conUsuario(ctx context.Context, u *cuentas.Usuario, t *cuentas.Token) context.Context {
	ctx = context.WithValue(ctx, claveUsuario, u)
	return context.WithValue(ctx, claveToken, t)
}

// UsuarioDe devuelve quién hizo el pedido, si vino con un token válido.
func UsuarioDe(ctx context.Context) *cuentas.Usuario {
	u, _ := ctx.Value(claveUsuario).(*cuentas.Usuario)
	return u
}

// limitador reparte permisos por IP con un cubo que se rellena solo.
type limitador struct {
	porMinuto int
	mu        sync.Mutex
	cubos     map[string]*cubo
}

type cubo struct {
	fichas float64
	ultimo time.Time
}

func nuevoLimitador(porMinuto int) *limitador {
	l := &limitador{porMinuto: porMinuto, cubos: map[string]*cubo{}}
	go l.limpiar()
	return l
}

// limpiar descarta los cubos llenos y viejos para no crecer sin techo.
func (l *limitador) limpiar() {
	for range time.Tick(5 * time.Minute) {
		l.mu.Lock()
		for ip, c := range l.cubos {
			if time.Since(c.ultimo) > 10*time.Minute {
				delete(l.cubos, ip)
			}
		}
		l.mu.Unlock()
	}
}

// permitir devuelve si el pedido pasa y cuántas fichas quedan.
func (l *limitador) permitir(ip string) (bool, int) {
	return l.permitirCon(ip, l.porMinuto)
}

// permitirCon usa un cupo propio para ese cubo: una IP anónima y un token
// identificado no tienen por qué tener el mismo.
func (l *limitador) permitirCon(quien string, porMinuto int) (bool, int) {
	if porMinuto <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	ip := quien
	max := float64(porMinuto)
	c, ok := l.cubos[ip]
	if !ok {
		c = &cubo{fichas: max, ultimo: time.Now()}
		l.cubos[ip] = c
	}
	ahora := time.Now()
	c.fichas += ahora.Sub(c.ultimo).Minutes() * max
	if c.fichas > max {
		c.fichas = max
	}
	c.ultimo = ahora

	if c.fichas < 1 {
		return false, 0
	}
	c.fichas--
	return true, int(c.fichas)
}

func ipDe(r *http.Request) string {
	// EasyPanel (Traefik) pone el cliente real acá.
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			v = v[:i]
		}
		if ip := strings.TrimSpace(v); ip != "" {
			return ip
		}
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// conCORS deja que cualquiera lea la API desde el navegador: es abierta.
func conCORS(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, If-None-Match")
		h.Set("Access-Control-Expose-Headers", "ETag, X-RateLimit-Limit, X-RateLimit-Remaining")
		h.Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		siguiente.ServeHTTP(w, r)
	})
}

type registrador struct {
	http.ResponseWriter
	codigo int
	bytes  int
}

func (r *registrador) WriteHeader(c int) {
	r.codigo = c
	r.ResponseWriter.WriteHeader(c)
}

func (r *registrador) Write(b []byte) (int, error) {
	if r.codigo == 0 {
		r.codigo = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func conLog(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inicio := time.Now()
		reg := &registrador{ResponseWriter: w}
		siguiente.ServeHTTP(reg, r)
		if reg.codigo == 0 {
			reg.codigo = http.StatusOK
		}
		slog.Info("pedido",
			"metodo", r.Method,
			"ruta", r.URL.Path,
			"query", r.URL.RawQuery,
			"codigo", reg.codigo,
			"bytes", reg.bytes,
			"ms", time.Since(inicio).Milliseconds(),
			"ip", ipDe(r),
		)
	})
}

// conPanico evita que un error propio tire el servidor entero.
func conPanico(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("pánico atendiendo un pedido", "panico", p, "ruta", r.URL.Path)
				escribirError(w, r, http.StatusInternalServerError, OrigenNotarum,
					"error interno", "")
			}
		}()
		siguiente.ServeHTTP(w, r)
	})
}
