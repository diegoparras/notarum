package alertas

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/diegoparras/notarum/internal/almacen"
)

// buscadorDeMentira devuelve lo que se le diga, para poder probar la pasada
// sin catálogos de verdad.
type buscadorDeMentira struct {
	mu        sync.Mutex
	resultado []Coincidencia
	err       error
	veces     int
}

func (b *buscadorDeMentira) BuscarParaAlerta(context.Context, Fuente, Criterios) ([]Coincidencia, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.veces++
	return b.resultado, b.err
}

func registroDePrueba(t *testing.T) *Registro {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { alm.Cerrar() })
	return NuevoRegistro(alm)
}

func TestUnaAlertaAvisaSoloLoNuevoYUnaSolaVez(t *testing.T) {
	t.Setenv(permitirPrivadasVar, "1")
	var recibidos []Aviso
	var mu sync.Mutex
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a Aviso
		json.NewDecoder(r.Body).Decode(&a)
		mu.Lock()
		recibidos = append(recibidos, a)
		mu.Unlock()
	}))
	defer destino.Close()

	reg := registroDePrueba(t)
	if _, err := reg.Crear(Alerta{
		Dueño: "diego", Nombre: "ENACOM", Fuente: FuenteNacional,
		Criterios: Criterios{Texto: "enacom"}, Webhook: destino.URL,
	}); err != nil {
		t.Fatal(err)
	}

	b := &buscadorDeMentira{resultado: []Coincidencia{{ID: "1", Titulo: "Res 1"}}}
	c := NuevoCorredor(reg, b, "https://notarum.example")

	// Primera pasada: se anota lo que ya estaba y no se avisa nada. Estrenar
	// una alerta no puede mandar de golpe todo lo que existe.
	if _, err := c.Correr(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	cuantos := len(recibidos)
	mu.Unlock()
	if cuantos != 0 {
		t.Fatalf("la primera pasada avisó %d veces", cuantos)
	}

	// Aparece algo nuevo: eso sí se avisa.
	b.resultado = []Coincidencia{{ID: "1", Titulo: "Res 1"}, {ID: "2", Titulo: "Res 2"}}
	r, err := c.Correr(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Novedades != 1 || r.Avisadas != 1 {
		t.Errorf("resumen = %+v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recibidos) != 1 {
		t.Fatalf("llegaron %d avisos", len(recibidos))
	}
	av := recibidos[0]
	if av.Alerta != "ENACOM" || len(av.Novedades) != 1 || av.Novedades[0].ID != "2" {
		t.Errorf("el aviso llegó como %+v", av)
	}
	if av.Instancia != "https://notarum.example" {
		t.Errorf("no dice de qué instancia salió: %q", av.Instancia)
	}

	// Y la pasada siguiente, sin nada nuevo, no vuelve a avisar.
	if _, err := c.Correr(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(recibidos) != 1 {
		t.Errorf("repitió el aviso: %d", len(recibidos))
	}
}

// Una alerta que falla no puede frenar a las demás, y su error tiene que
// quedar donde lo vea quien la creó.
func TestUnaAlertaQueFallaNoFrenaALasOtras(t *testing.T) {
	reg := registroDePrueba(t)
	rota, err := reg.Crear(Alerta{
		Dueño: "diego", Nombre: "rota", Fuente: FuenteNacional,
		Criterios: Criterios{Texto: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}

	b := &buscadorDeMentira{err: errors.New("el catálogo no se bajó")}
	c := NuevoCorredor(reg, b, "")
	r, err := c.Correr(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Fallaron != 1 || r.Corridas != 1 {
		t.Errorf("resumen = %+v", r)
	}
	guardada, _ := reg.Leer(rota.ID)
	if !strings.Contains(guardada.Error, "no se bajó") {
		t.Errorf("el error quedó como %q", guardada.Error)
	}
	if guardada.UltimaCorrida.IsZero() {
		t.Error("no quedó anotado que corrió")
	}
}

// Lo encontrado no se pierde porque el webhook no ande: queda en la alerta.
func TestSiElWebhookFallaLoEncontradoQueda(t *testing.T) {
	t.Setenv(permitirPrivadasVar, "1")
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	defer destino.Close()

	reg := registroDePrueba(t)
	a, _ := reg.Crear(Alerta{
		Dueño: "diego", Nombre: "con webhook roto", Fuente: FuenteNacional,
		Criterios: Criterios{Texto: "x"}, Webhook: destino.URL,
	})
	b := &buscadorDeMentira{resultado: []Coincidencia{{ID: "1"}}}
	c := NuevoCorredor(reg, b, "")

	c.Correr(context.Background(), nil) // la primera anota lo que había
	b.resultado = append(b.resultado, Coincidencia{ID: "2", Titulo: "algo nuevo"})
	c.Correr(context.Background(), nil)

	guardada, _ := reg.Leer(a.ID)
	if len(guardada.Ultimas) == 0 || guardada.Ultimas[0].ID != "2" {
		t.Errorf("lo encontrado no quedó guardado: %+v", guardada.Ultimas)
	}
	if !strings.Contains(guardada.Error, "webhook") {
		t.Errorf("no dice que falló el aviso: %q", guardada.Error)
	}
}

// Sin webhook, la alerta igual sirve: lo encontrado se ve en la cuenta.
func TestUnaAlertaSinWebhookGuardaLoQueEncuentra(t *testing.T) {
	reg := registroDePrueba(t)
	a, _ := reg.Crear(Alerta{
		Dueño: "diego", Nombre: "sin webhook", Fuente: FuenteProvincial,
		Criterios: Criterios{Texto: "agua"},
	})
	b := &buscadorDeMentira{resultado: []Coincidencia{{ID: "1"}}}
	c := NuevoCorredor(reg, b, "")
	c.Correr(context.Background(), nil)
	b.resultado = append(b.resultado, Coincidencia{ID: "2", Titulo: "Ley de aguas"})
	c.Correr(context.Background(), nil)

	guardada, _ := reg.Leer(a.ID)
	if guardada.Avisados != 1 || len(guardada.Ultimas) != 1 {
		t.Errorf("quedó %+v", guardada)
	}
	if guardada.Error != "" {
		t.Errorf("dio error sin webhook: %q", guardada.Error)
	}
}

func TestBorrarUnaAlertaAjenaNoSePuede(t *testing.T) {
	reg := registroDePrueba(t)
	a, _ := reg.Crear(Alerta{
		Dueño: "diego", Nombre: "mía", Fuente: FuenteNacional,
		Criterios: Criterios{Texto: "x"},
	})
	if err := reg.Borrar(a.ID, "otro"); err == nil {
		t.Error("otro pudo borrarla")
	}
	if err := reg.Borrar(a.ID, "diego"); err != nil {
		t.Errorf("el dueño no pudo borrarla: %v", err)
	}
	if len(reg.De("diego")) != 0 || len(reg.Todas()) != 0 {
		t.Error("quedó en algún índice después de borrarla")
	}
}

func TestHayUnTopeDeAlertasPorCuenta(t *testing.T) {
	reg := registroDePrueba(t)
	for i := 0; i < MaximoPorCuenta; i++ {
		if _, err := reg.Crear(Alerta{
			Dueño: "diego", Nombre: "una", Fuente: FuenteNacional,
			Criterios: Criterios{Texto: "x"},
		}); err != nil {
			t.Fatalf("en la %d: %v", i, err)
		}
	}
	if _, err := reg.Crear(Alerta{
		Dueño: "diego", Nombre: "una más", Fuente: FuenteNacional,
		Criterios: Criterios{Texto: "x"},
	}); err == nil {
		t.Error("se pasó del tope")
	}
}
