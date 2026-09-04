package cuentas

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestClaveSeVerifica(t *testing.T) {
	c, err := NuevaClave("una frase larga y tranquila")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Verificar("una frase larga y tranquila") {
		t.Error("la clave correcta no verificó")
	}
	if c.Verificar("una frase larga y tranquil") {
		t.Error("verificó una clave distinta")
	}
	if c.Verificar("") {
		t.Error("verificó una clave vacía")
	}
}

// La clave no puede quedar guardada en ningún lado en claro, ni siquiera
// serializada: es lo que se va a un archivo o a una base.
func TestLaClaveNoSeGuardaEnClaro(t *testing.T) {
	const secreta = "mi frase secreta de verdad"
	c, err := NuevaClave(secreta)
	if err != nil {
		t.Fatal(err)
	}
	crudo, err := json.Marshal(Usuario{Nombre: "diego", Rol: RolAdmin, Clave: c})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(crudo), secreta) {
		t.Fatalf("la clave quedó en claro: %s", crudo)
	}
	// Y tampoco en el propio struct.
	if strings.Contains(c.Hash, secreta) || strings.Contains(c.Sal, secreta) {
		t.Error("la clave aparece en el hash o en la sal")
	}
}

// Dos veces la misma clave tienen que dar hashes distintos: si no, se ve quién
// comparte contraseña con quién.
func TestCadaClaveTieneSuSal(t *testing.T) {
	a, err := NuevaClave("la misma clave de siempre")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NuevaClave("la misma clave de siempre")
	if err != nil {
		t.Fatal(err)
	}
	if a.Sal == b.Sal {
		t.Error("dos claves compartieron la sal")
	}
	if a.Hash == b.Hash {
		t.Error("dos claves con la misma contraseña dieron el mismo hash")
	}
	// Y las dos siguen verificando.
	if !a.Verificar("la misma clave de siempre") || !b.Verificar("la misma clave de siempre") {
		t.Error("alguna dejó de verificar")
	}
}

// Una clave corta es la puerta más fácil de forzar.
func TestValidarClave(t *testing.T) {
	cortas := []string{"", "  ", "corta", "12345678901", strings.Repeat(" ", 20)}
	for _, c := range cortas {
		if err := ValidarClave(c); err == nil {
			t.Errorf("se aceptó la clave %q", c)
		}
		if _, err := NuevaClave(c); err == nil {
			t.Errorf("se creó una clave con %q", c)
		}
	}
	for _, c := range []string{"doce caracte", "una frase larga y tranquila", "contraseña con ñ y tildes áéí"} {
		if err := ValidarClave(c); err != nil {
			t.Errorf("se rechazó %q: %v", c, err)
		}
	}
}

// Un registro corrupto o vacío no puede dejar entrar a nadie.
func TestClaveVaciaNoVerificaNada(t *testing.T) {
	for _, c := range []Clave{
		{},
		{Hash: "x", Iteraciones: 0},
		{Hash: "no-es-base64!!", Sal: "tampoco!!", Iteraciones: 1000},
		{Algoritmo: "pbkdf2-sha256", Iteraciones: 600000},
	} {
		if c.Verificar("") || c.Verificar("cualquier cosa") {
			t.Errorf("la clave %+v dejó entrar", c)
		}
	}
}

// El número de iteraciones viaja con la clave, así que se puede subir sin
// invalidar las que ya existen.
func TestClaveConIteracionesPropias(t *testing.T) {
	c, err := NuevaClave("una frase larga y tranquila")
	if err != nil {
		t.Fatal(err)
	}
	if c.Iteraciones < 100000 {
		t.Errorf("iteraciones = %d: muy pocas para una clave", c.Iteraciones)
	}
	if c.Algoritmo == "" {
		t.Error("no se guardó con qué algoritmo se derivó")
	}
}

func TestValidarNombre(t *testing.T) {
	malos := []string{"", "ab", "Diego", "con espacio", "conñ", "a@b", strings.Repeat("x", 33), "MAYUS"}
	for _, n := range malos {
		if err := ValidarNombre(n); err == nil {
			t.Errorf("se aceptó el nombre %q", n)
		}
	}
	for _, n := range []string{"diego", "diego.parras", "notarum-bot", "usuario_1", "abc"} {
		if err := ValidarNombre(n); err != nil {
			t.Errorf("se rechazó %q: %v", n, err)
		}
	}
}

func TestNormalizarNombre(t *testing.T) {
	for entrada, esperado := range map[string]string{
		"Diego": "diego", "  diego  ": "diego", "DIEGO": "diego", "diego": "diego",
	} {
		if got := NormalizarNombre(entrada); got != esperado {
			t.Errorf("%q -> %q, se esperaba %q", entrada, got, esperado)
		}
	}
}

// El token se muestra una vez; lo que queda guardado es su huella.
func TestGenerarToken(t *testing.T) {
	valor, huella, prefijo, err := GenerarToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(valor, PrefijoToken) {
		t.Errorf("el token no lleva el prefijo que lo identifica: %q", valor)
	}
	if len(valor) < 40 {
		t.Errorf("el token es corto (%d): poca entropía", len(valor))
	}
	if huella == "" || strings.Contains(huella, valor) {
		t.Error("la huella no puede contener el token")
	}
	if !strings.HasPrefix(valor, prefijo) || len(prefijo) >= len(valor) {
		t.Errorf("prefijo = %q para el token %q", prefijo, valor)
	}
	// La huella tiene que reproducirse a partir del valor, y sólo de ese.
	if Huella(valor) != huella {
		t.Error("la huella no es reproducible")
	}
	otro, _, _, _ := GenerarToken()
	if Huella(otro) == huella {
		t.Error("dos tokens distintos dieron la misma huella")
	}
}

func TestTokensSonUnicos(t *testing.T) {
	vistos := map[string]bool{}
	for i := 0; i < 200; i++ {
		v, _, _, err := GenerarToken()
		if err != nil {
			t.Fatal(err)
		}
		if vistos[v] {
			t.Fatalf("se repitió un token en la iteración %d", i)
		}
		vistos[v] = true
	}
}

func TestTokenDeCabecera(t *testing.T) {
	casos := map[string]string{
		"Bearer ntrm_abc":    "ntrm_abc",
		"bearer ntrm_abc":    "ntrm_abc",
		"BEARER  ntrm_abc ":  "ntrm_abc",
		"":                   "",
		"ntrm_abc":           "", // sin el esquema no vale
		"Basic dXNlcjpwYXNz": "",
		"Bearer":             "",
		"Bearer ":            "",
	}
	for cabecera, esperado := range casos {
		if got := TokenDeCabecera(cabecera); got != esperado {
			t.Errorf("%q -> %q, se esperaba %q", cabecera, got, esperado)
		}
	}
}

func TestRolesYAlcances(t *testing.T) {
	if !RolAdmin.Valido() || !RolPersona.Valido() {
		t.Error("un rol conocido se rechazó")
	}
	for _, r := range []Rol{"", "root", "Admin", "superusuario"} {
		if r.Valido() {
			t.Errorf("se aceptó el rol %q", r)
		}
	}
	if !AlcanceAPI.Valido() || !AlcanceMCP.Valido() {
		t.Error("un alcance conocido se rechazó")
	}
	for _, a := range []Alcance{"", "todo", "API", "admin"} {
		if a.Valido() {
			t.Errorf("se aceptó el alcance %q", a)
		}
	}
}

func TestTokenActivo(t *testing.T) {
	if !(Token{}).Activo() {
		t.Error("un token sin revocar tendría que estar activo")
	}
	ahora := time.Now()
	if (Token{Revocado: &ahora}).Activo() {
		t.Error("un token revocado no puede seguir activo")
	}
}

// Una clave impresa por accidente no puede llevarse el hash ni la sal: un %v
// en un log o un {{.}} en una plantilla no tiene que costar la cuenta.
func TestUnaClaveNoSeImprime(t *testing.T) {
	c, err := NuevaClave("una frase larga y tranquila")
	if err != nil {
		t.Fatal(err)
	}
	u := &Usuario{Nombre: "diego", Rol: RolPersona, Clave: c}
	for que, texto := range map[string]string{
		"la clave":   fmt.Sprintf("%v", u.Clave),
		"con +v":     fmt.Sprintf("%+v", u.Clave),
		"el usuario": fmt.Sprintf("%v", *u),
	} {
		if strings.Contains(texto, u.Clave.Hash) {
			t.Errorf("%s deja ver el hash: %s", que, texto)
		}
		if strings.Contains(texto, u.Clave.Sal) {
			t.Errorf("%s deja ver la sal: %s", que, texto)
		}
	}
	// Y sin embargo se sigue guardando entera: esto es sólo para imprimir.
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), u.Clave.Hash) {
		t.Error("el hash no se guarda; sin él no se puede volver a entrar")
	}
}
