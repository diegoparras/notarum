package memoria

import "testing"

func TestEnBytes(t *testing.T) {
	for entrada, esperado := range map[string]int64{
		"512MB": 512 << 20, "512mb": 512 << 20, "512M": 512 << 20,
		"1GB": 1 << 30, "1g": 1 << 30, "1GiB": 1 << 30,
		"1.5GB": int64(1.5 * (1 << 30)), "  2 GB  ": 2 << 30,
		"1048576": 1 << 20, "1024KB": 1 << 20,
	} {
		got, err := enBytes(entrada)
		if err != nil || got != esperado {
			t.Errorf("%q -> %d (%v), se esperaba %d", entrada, got, err, esperado)
		}
	}
	for _, malo := range []string{"", "mucha", "GB", "-1", "1 giga"} {
		if v, err := enBytes(malo); err == nil && v > 0 {
			t.Errorf("se aceptó %q -> %d", malo, v)
		}
	}
}

// Los archivos del cgroup dicen "max" cuando no hay tope, y algunos sistemas
// ponen un número enorme que significa lo mismo.
func TestLeerLimite(t *testing.T) {
	for entrada, esperado := range map[string]int64{
		"536870912\n": 536870912,
		"max":         0,
		"max\n":       0,
		"":            0,
		"  ":          0,
		"0":           0,
		"-1":          0,
		// Lo que pone un sistema sin límite: hay que tratarlo como si no
		// hubiera ninguno, o el recolector trabajaría contra un techo falso.
		"9223372036854771712": 0,
		"no es un numero":     0,
	} {
		if got := leerLimite(entrada); got != esperado {
			t.Errorf("%q -> %d, se esperaba %d", entrada, got, esperado)
		}
	}
}

// Sin límite del contenedor y sin configuración, no se toca nada: Go sabe
// cuidarse solo cuando no hay un techo del que enterarse.
func TestSinLimiteNoSeAjusta(t *testing.T) {
	if got := Ajustar(""); got != 0 && DelContenedor() == 0 {
		t.Errorf("se fijó un límite de %d sin que hubiera ninguno", got)
	}
}

// Lo configurado a mano manda sobre lo que diga el contenedor.
func TestLoConfiguradoManda(t *testing.T) {
	got := Ajustar("256MB")
	if got != 256<<20 {
		t.Errorf("límite = %d, se esperaba %d", got, int64(256<<20))
	}
	// Y se deja como estaba para no afectar al resto de los tests.
	Ajustar("8GB")
}
