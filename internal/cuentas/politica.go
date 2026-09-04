package cuentas

import (
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
