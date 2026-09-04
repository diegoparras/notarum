package almacen

import "testing"

func TestArmarDSN(t *testing.T) {
	dsn, err := ArmarDSN(DatosConexion{
		Host: "postgres", Base: "notarum", Usuario: "notarum", Clave: "secreto",
	})
	if err != nil {
		t.Fatal(err)
	}
	esperado := "postgres://notarum:secreto@postgres:5432/notarum?sslmode=disable"
	if dsn != esperado {
		t.Errorf("dsn = %q\n  se esperaba %q", dsn, esperado)
	}
}

// Una clave con caracteres especiales tiene que sobrevivir: es un error
// clásico y el driver no explica bien qué pasó.
func TestArmarDSNEscapaLaClave(t *testing.T) {
	dsn, err := ArmarDSN(DatosConexion{
		Host: "db.interno", Puerto: "6432", Base: "notarum",
		Usuario: "notarum", Clave: "p@ss/w#rd?x", SSLMode: "require",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Los caracteres peligrosos van escapados, no crudos.
	for _, crudo := range []string{"p@ss/w#rd?x"} {
		if contieneTexto(dsn, crudo) {
			t.Errorf("la clave quedó sin escapar en %q", dsn)
		}
	}
	if !contieneTexto(dsn, "sslmode=require") || !contieneTexto(dsn, "db.interno:6432") {
		t.Errorf("dsn = %q", dsn)
	}
}

func TestArmarDSNFaltantes(t *testing.T) {
	if _, err := ArmarDSN(DatosConexion{Base: "notarum"}); err == nil {
		t.Error("se aceptó sin host")
	}
	if _, err := ArmarDSN(DatosConexion{Host: "postgres"}); err == nil {
		t.Error("se aceptó sin base")
	}
}

// La cadena se muestra en logs y en /v1/salud: la clave no puede salir.
func TestOcultarClave(t *testing.T) {
	casos := map[string]string{
		"postgres://notarum:secreto@host:5432/base?sslmode=disable": "postgres://notarum:%C2%B7%C2%B7%C2%B7@host:5432/base?sslmode=disable",
		"postgres://host:5432/base":                                 "postgres://host:5432/base",
		"no es una url":                                             "no es una url",
	}
	for entrada, esperado := range casos {
		if got := OcultarClave(entrada); got != esperado {
			t.Errorf("OcultarClave(%q) = %q\n  se esperaba %q", entrada, got, esperado)
		}
	}
	if contieneTexto(OcultarClave("postgres://u:secreto@h/b"), "secreto") {
		t.Error("la clave se filtró")
	}
}

func contieneTexto(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
