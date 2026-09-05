package cuentas

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Modo define qué puede hacer alguien que no se identificó.
//
// Es una decisión de quien opera la instancia, no del producto: una cátedra
// puede querer su copia abierta a cualquiera, un estudio puede querer la suya
// cerrada, y un organismo puede querer el lector público pero la API con
// token. Las tres son legítimas.
type Modo string

const (
	// ModoAbierto: leer no pide nada. Las cuentas sólo sirven para tener más
	// cuota y para el MCP.
	ModoAbierto Modo = "abierto"
	// ModoMixto: el lector web es público, la API y el MCP piden token.
	ModoMixto Modo = "mixto"
	// ModoCerrado: nada se lee sin sesión o token.
	ModoCerrado Modo = "cerrado"
)

func (m Modo) Valido() bool {
	return m == ModoAbierto || m == ModoMixto || m == ModoCerrado
}

// ParseModo lee el modo de la configuración.
func ParseModo(s string) (Modo, error) {
	m := Modo(strings.ToLower(strings.TrimSpace(s)))
	if !m.Valido() {
		return "", fmt.Errorf("modo de acceso %q inválido: se esperaba abierto, mixto o cerrado", s)
	}
	return m, nil
}

// Politica reúne el modo y las cuotas. Todo sale de la configuración de la
// instancia, con valores por defecto que sirven para arrancar.
type Politica struct {
	Modo Modo
	// PorMinuto es la cuota de cada rol, más la de quien no se identificó.
	Anonimo int
	Persona int
	Admin   int
	// Lector es la cuota de las páginas web. Va aparte porque una persona
	// mirando el sitio hace muchos más pedidos que un programa bien escrito, y
	// mezclarlas deja la interfaz inusable apenas se baja el límite de la API.
	Lector int
	// Login es el tope de intentos de entrada por minuto y por dirección. Es
	// el único límite que existe para frenar a alguien, no para repartir.
	Login int
}

// PoliticaPorDefecto es de dónde se parte cuando no se configura nada.
//
// El modo depende de si hay cuentas: sin cuentas no hay con qué autenticarse,
// así que cerrar dejaría la instancia inaccesible; con cuentas creadas, lo
// prudente es cerrar y que el operador abra si quiere.
func PoliticaPorDefecto(hayUsuarios bool) Politica {
	modo := ModoAbierto
	if hayUsuarios {
		modo = ModoCerrado
	}
	return Politica{
		Modo:    modo,
		Anonimo: 60,
		Persona: 600,
		Admin:   6000,
		Lector:  600,
		Login:   10,
	}
}

// CuotaDe devuelve los pedidos por minuto de quien hace el pedido. Con nil
// —nadie identificado— vale la cuota anónima.
func (p Politica) CuotaDe(u *Usuario) int {
	if u == nil {
		return p.Anonimo
	}
	switch u.Rol {
	case RolAdmin:
		return p.Admin
	case RolPersona:
		return p.Persona
	}
	return p.Anonimo
}

// PermiteAPI dice si este pedido a la API puede seguir.
func (p Politica) PermiteAPI(u *Usuario) bool {
	if u != nil {
		return true
	}
	return p.Modo == ModoAbierto
}

// PermiteLector dice si estas páginas se pueden ver.
func (p Politica) PermiteLector(u *Usuario) bool {
	if u != nil {
		return true
	}
	return p.Modo == ModoAbierto || p.Modo == ModoMixto
}

// PermiteMCP dice si se puede usar el MCP. El MCP es siempre para programas,
// así que sigue la misma regla que la API.
func (p Politica) PermiteMCP(u *Usuario) bool { return p.PermiteAPI(u) }

// Explicacion dice en una frase qué deja hacer esta política, para mostrarlo
// en la documentación y en el estado.
func (p Politica) Explicacion() string {
	switch p.Modo {
	case ModoAbierto:
		return "cualquiera puede leer sin cuenta; los tokens sirven para tener más cuota"
	case ModoMixto:
		return "el lector web es público; la API y el MCP piden un token"
	case ModoCerrado:
		return "hay que entrar o traer un token para cualquier cosa"
	}
	return ""
}

// ------------------------------------------------------- la política vigente

// clavePolitica es donde vive la que se configuró desde el panel.
const clavePolitica = "cuentas/_politica"

// Politica devuelve la que rige ahora.
//
// Se guarda en memoria y se relee sola: la del entorno es el punto de
// partida, y lo que se cambie desde el panel la pisa. Así quien opera puede
// abrir o cerrar la instancia sin editar variables y reiniciar.
func (r *Registro) Politica() Politica {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.politica
}

// FijarPolitica cambia la que rige y la guarda.
func (r *Registro) FijarPolitica(p Politica) error {
	if !p.Modo.Valido() {
		return fmt.Errorf("modo de acceso inválido: %q", p.Modo)
	}
	if err := p.Revisar(); err != nil {
		return err
	}
	crudo, err := json.Marshal(p)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.alm.Guardar(clavePolitica, crudo, sinVencimiento); err != nil {
		return err
	}
	r.politica = p
	return nil
}

// CargarPolitica arranca con lo que haya guardado, y si no hay, con la que
// venga de la configuración.
func (r *Registro) CargarPolitica(delEntorno Politica) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.politica = delEntorno
	crudo, hay := r.alm.Leer(clavePolitica)
	if !hay {
		return
	}
	var guardada Politica
	if err := json.Unmarshal(crudo, &guardada); err != nil || !guardada.Modo.Valido() {
		return
	}
	r.politica = guardada
}

// HayPoliticaGuardada dice si lo que rige se configuró desde el panel, para
// poder aclararlo al lado de los valores.
func (r *Registro) HayPoliticaGuardada() bool {
	return r.alm.Existe(clavePolitica)
}

// OlvidarPolitica borra lo configurado y vuelve a lo que diga el entorno.
func (r *Registro) OlvidarPolitica(delEntorno Politica) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.alm.Borrar(clavePolitica); err != nil {
		return err
	}
	r.politica = delEntorno
	return nil
}

// Revisar dice si los números tienen sentido antes de guardarlos. Una cuota
// en cero deja a todos afuera sin que nadie entienda por qué.
func (p Politica) Revisar() error {
	for _, c := range []struct {
		nombre string
		valor  int
	}{
		{"la cuota de quien no se identifica", p.Anonimo},
		{"la cuota de las cuentas", p.Persona},
		{"la cuota de quien administra", p.Admin},
		{"la cuota del lector web", p.Lector},
		{"el tope de intentos de entrada", p.Login},
	} {
		if c.valor < 1 {
			return fmt.Errorf("%s tiene que ser al menos 1", c.nombre)
		}
		if c.valor > 1000000 {
			return fmt.Errorf("%s es un número imposible", c.nombre)
		}
	}
	return nil
}
