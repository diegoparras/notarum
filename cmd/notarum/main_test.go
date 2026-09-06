package main

import "testing"

// Una variable de sí o no tiene que decir que sí cuando dice que sí.
//
// Con cualquier valor no vacío contando como sí, NOTARUM_SIN_MCP=0 apagaba el
// MCP y NOTARUM_BUSCADOR_INFOLEG=false lo encendía: lo contrario exacto de lo
// que quiso escribir quien lo escribió. Es de los errores que no se encuentran
// nunca, porque el archivo de configuración dice lo que uno quería.
func TestEncendido(t *testing.T) {
	const clave = "NOTARUM_UNA_PRUEBA"

	// Sin definir, rige lo que traiga por defecto.
	for _, porDefecto := range []bool{true, false} {
		t.Setenv(clave, "")
		if got := encendido(clave, porDefecto); got != porDefecto {
			t.Errorf("sin definir con defecto %v dio %v", porDefecto, got)
		}
	}

	for valor, esperado := range map[string]bool{
		"1": true, "si": true, "sí": true, "true": true, "on": true,
		"yes": true, "y": true, "encendido": true, "TRUE": true, " Sí ": true,
		"0": false, "no": false, "false": false, "off": false, "n": false,
		"apagado": false, "FALSE": false, " no ": false,
		// Lo que no se entiende se toma como sí: quien se tomó el trabajo de
		// escribir la variable quiso encenderla.
		"cualquier cosa": true,
	} {
		t.Setenv(clave, valor)
		if got := encendido(clave, false); got != esperado {
			t.Errorf("%q dio %v, se esperaba %v", valor, got, esperado)
		}
	}
}
