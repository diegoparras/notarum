package cuentas

import (
	"errors"
	"strings"
	"testing"
)

func TestElCodigoDeArranqueEsEstable(t *testing.T) {
	r, _ := nuevoRegistro(t)
	uno, err := r.CodigoDeArranque()
	if err != nil {
		t.Fatal(err)
	}
	if len(uno) != LargoCodigo {
		t.Fatalf("el código tiene %d caracteres", len(uno))
	}
	// El mismo entre llamadas: si cambiara, el que quedó anotado en el log
	// dejaría de servir justo cuando hace falta.
	otro, _ := r.CodigoDeArranque()
	if uno != otro {
		t.Errorf("el código cambió: %q y después %q", uno, otro)
	}
	// Sin letras que se confundan al copiarlo a mano.
	for _, c := range "01IO" {
		if strings.ContainsRune(uno, c) {
			t.Errorf("el código trae %q, que se confunde al copiarlo: %s", c, uno)
		}
	}
}

// Dos instancias distintas no pueden tener el mismo código.
func TestCadaInstanciaTieneElSuyo(t *testing.T) {
	vistos := map[string]bool{}
	for i := 0; i < 30; i++ {
		r, _ := nuevoRegistro(t)
		c, err := r.CodigoDeArranque()
		if err != nil {
			t.Fatal(err)
		}
		if vistos[c] {
			t.Fatalf("se repitió el código %q", c)
		}
		vistos[c] = true
	}
}

func TestArrancarCreaLaPrimeraCuenta(t *testing.T) {
	r, _ := nuevoRegistro(t)
	codigo, _ := r.CodigoDeArranque()

	u, err := r.Arrancar("diego", "una frase larga y tranquila", codigo)
	if err != nil {
		t.Fatal(err)
	}
	// La primera administra: es la que va a poner en marcha todo lo demás.
	if u.Rol != RolAdmin {
		t.Errorf("rol = %q", u.Rol)
	}
	if !r.HayUsuarios() {
		t.Error("después de arrancar sigue diciendo que no hay cuentas")
	}
	// Y entra por el formulario, como cualquier otra.
	if _, err := r.Autenticar("diego", "una frase larga y tranquila"); err != nil {
		t.Errorf("no se pudo entrar con la cuenta recién creada: %v", err)
	}
}

// El código se perdona como se copia: en minúsculas, con guiones, con espacios.
func TestElCodigoSePerdona(t *testing.T) {
	r, _ := nuevoRegistro(t)
	codigo, _ := r.CodigoDeArranque()

	for _, forma := range []string{
		strings.ToLower(codigo),
		CodigoLegible(codigo),
		strings.ToLower(CodigoLegible(codigo)),
		"  " + codigo + "  ",
	} {
		r2, _ := nuevoRegistro(t)
		// Cada uno con su registro: arrancar cierra la puerta.
		suyo, _ := r2.CodigoDeArranque()
		var probar string
		switch forma {
		case strings.ToLower(codigo):
			probar = strings.ToLower(suyo)
		case CodigoLegible(codigo):
			probar = CodigoLegible(suyo)
		case strings.ToLower(CodigoLegible(codigo)):
			probar = strings.ToLower(CodigoLegible(suyo))
		default:
			probar = "  " + suyo + "  "
		}
		if _, err := r2.Arrancar("diego", "una frase larga y tranquila", probar); err != nil {
			t.Errorf("no se aceptó %q: %v", probar, err)
		}
	}
}

// La puerta se abre una sola vez, y no se vuelve a abrir.
func TestLaPuertaSeCierra(t *testing.T) {
	r, _ := nuevoRegistro(t)
	codigo, _ := r.CodigoDeArranque()
	if _, err := r.Arrancar("diego", "una frase larga y tranquila", codigo); err != nil {
		t.Fatal(err)
	}
	// Ni con el mismo código.
	if _, err := r.Arrancar("otro", "otra frase larga y tranquila", codigo); !errors.Is(err, ErrYaHayCuentas) {
		t.Errorf("se pudo arrancar de nuevo: %v", err)
	}
	// Y ya no hay código que pedir.
	if c, _ := r.CodigoDeArranque(); c != "" {
		t.Errorf("sigue habiendo código de arranque: %q", c)
	}
}

func TestArrancarConElCodigoEquivocado(t *testing.T) {
	for _, malo := range []string{"", "NOESESTE", "AAAAAAAAAAAAAAAA", "1234"} {
		r, _ := nuevoRegistro(t)
		if _, err := r.CodigoDeArranque(); err != nil {
			t.Fatal(err)
		}
		_, err := r.Arrancar("diego", "una frase larga y tranquila", malo)
		if !errors.Is(err, ErrCodigoArranque) {
			t.Errorf("con %q el error fue %v", malo, err)
		}
		if r.HayUsuarios() {
			t.Errorf("con %q se creó la cuenta igual", malo)
		}
	}
}

// Una clave corta no pasa ni con el código bien: la primera cuenta es la que
// más importa.
func TestLaPrimeraCuentaTambienValidaLaClave(t *testing.T) {
	r, _ := nuevoRegistro(t)
	codigo, _ := r.CodigoDeArranque()
	if _, err := r.Arrancar("diego", "corta", codigo); err == nil {
		t.Fatal("se aceptó una clave corta")
	}
	if r.HayUsuarios() {
		t.Error("quedó creada igual")
	}
	// Y la puerta sigue abierta para volver a intentarlo bien.
	if _, err := r.Arrancar("diego", "una frase larga y tranquila", codigo); err != nil {
		t.Errorf("no se pudo reintentar: %v", err)
	}
}

func TestCodigoLegible(t *testing.T) {
	if got := CodigoLegible("ABCDEFGHJKLMNPQR"); got != "ABCD-EFGH-JKLM-NPQR" {
		t.Errorf("legible = %q", got)
	}
	// Y se puede volver.
	if got := NormalizarCodigo("ABCD-EFGH-JKLM-NPQR"); got != "ABCDEFGHJKLMNPQR" {
		t.Errorf("normalizado = %q", got)
	}
}
