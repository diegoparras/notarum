package cuentas

import "testing"

func TestParseModo(t *testing.T) {
	for entrada, esperado := range map[string]Modo{
		"abierto": ModoAbierto, "ABIERTO": ModoAbierto, " mixto ": ModoMixto, "cerrado": ModoCerrado,
	} {
		m, err := ParseModo(entrada)
		if err != nil || m != esperado {
			t.Errorf("%q -> %q, %v", entrada, m, err)
		}
	}
	for _, malo := range []string{"", "publico", "privado", "si", "todo"} {
		if _, err := ParseModo(malo); err == nil {
			t.Errorf("se aceptó el modo %q", malo)
		}
	}
}

// Sin cuentas creadas no hay con qué identificarse: cerrar dejaría la
// instancia inaccesible para todos, incluido quien la opera.
func TestPorDefectoSinCuentasEsAbierto(t *testing.T) {
	if p := PoliticaPorDefecto(false); p.Modo != ModoAbierto {
		t.Errorf("modo = %q", p.Modo)
	}
	// Y en cuanto hay cuentas, lo prudente es cerrar.
	if p := PoliticaPorDefecto(true); p.Modo != ModoCerrado {
		t.Errorf("modo = %q", p.Modo)
	}
}

// Cada modo tiene que dejar pasar exactamente lo que dice y nada más.
func TestQuePermiteCadaModo(t *testing.T) {
	anonimo := (*Usuario)(nil)
	persona := &Usuario{Nombre: "diego", Rol: RolPersona}

	casos := []struct {
		modo             Modo
		api, lector, mcp bool
	}{
		{ModoAbierto, true, true, true},
		{ModoMixto, false, true, false},
		{ModoCerrado, false, false, false},
	}
	for _, c := range casos {
		p := PoliticaPorDefecto(false)
		p.Modo = c.modo
		if got := p.PermiteAPI(anonimo); got != c.api {
			t.Errorf("%s: API para anónimo = %v, se esperaba %v", c.modo, got, c.api)
		}
		if got := p.PermiteLector(anonimo); got != c.lector {
			t.Errorf("%s: lector para anónimo = %v, se esperaba %v", c.modo, got, c.lector)
		}
		if got := p.PermiteMCP(anonimo); got != c.mcp {
			t.Errorf("%s: MCP para anónimo = %v, se esperaba %v", c.modo, got, c.mcp)
		}
		// Quien se identificó pasa siempre, en cualquier modo.
		if !p.PermiteAPI(persona) || !p.PermiteLector(persona) || !p.PermiteMCP(persona) {
			t.Errorf("%s: alguien identificado quedó afuera", c.modo)
		}
	}
}

func TestCuotaPorRol(t *testing.T) {
	p := PoliticaPorDefecto(false)
	if got := p.CuotaDe(nil); got != p.Anonimo {
		t.Errorf("anónimo = %d", got)
	}
	if got := p.CuotaDe(&Usuario{Rol: RolPersona}); got != p.Persona {
		t.Errorf("persona = %d", got)
	}
	if got := p.CuotaDe(&Usuario{Rol: RolAdmin}); got != p.Admin {
		t.Errorf("admin = %d", got)
	}
	// Un rol que no se reconoce no puede dar más cuota que la anónima.
	if got := p.CuotaDe(&Usuario{Rol: "inventado"}); got != p.Anonimo {
		t.Errorf("rol inventado = %d, tendría que caer en la cuota anónima", got)
	}
}

// El orden de las cuotas por defecto tiene que tener sentido.
func TestLasCuotasPorDefectoEscalan(t *testing.T) {
	p := PoliticaPorDefecto(false)
	if !(p.Anonimo < p.Persona && p.Persona < p.Admin) {
		t.Errorf("las cuotas no escalan: anónimo=%d persona=%d admin=%d", p.Anonimo, p.Persona, p.Admin)
	}
	// El lector necesita margen propio: mirar el sitio hace muchos más
	// pedidos que consultar la API desde un programa.
	if p.Lector < p.Anonimo {
		t.Errorf("el lector (%d) tiene menos margen que la API anónima (%d)", p.Lector, p.Anonimo)
	}
	// El login es el único límite pensado para frenar, no para repartir.
	if p.Login >= p.Anonimo {
		t.Errorf("el límite de login (%d) tendría que ser mucho más estricto", p.Login)
	}
}

func TestExplicacion(t *testing.T) {
	for _, m := range []Modo{ModoAbierto, ModoMixto, ModoCerrado} {
		p := PoliticaPorDefecto(false)
		p.Modo = m
		if p.Explicacion() == "" {
			t.Errorf("el modo %q no se explica", m)
		}
	}
}
