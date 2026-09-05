package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Un pedido que entra en pánico tiene que quedar registrado igual, y con su
// código de error.
//
// Escrito de la otra forma —la línea después de atender— el pánico desenrolla
// la pila sin pasar por el log, y queda una instancia que falla contra un
// registro que dice que nadie pidió nada. Es lo que hizo que un error en
// producción fuera invisible durante horas.
func TestUnPedidoQueEntraEnPanicoQuedaEnElLog(t *testing.T) {
	var salida bytes.Buffer
	anterior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&salida, nil)))
	defer slog.SetDefault(anterior)

	explota := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("se rompió algo adentro")
	})
	h := conLog(conPanico(explota))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/docs/generar", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("contestó %d y no un 500", w.Code)
	}

	var vioElPedido, vioElPanico bool
	for _, linea := range strings.Split(strings.TrimSpace(salida.String()), "\n") {
		var l map[string]any
		if json.Unmarshal([]byte(linea), &l) != nil {
			continue
		}
		switch l["msg"] {
		case "pedido":
			vioElPedido = true
			// Con su 500, no con un 200 ni con un 0: el pedido falló y el
			// registro tiene que decirlo. Es lo que da el orden de la cadena,
			// con conPanico por dentro de conLog.
			if c, _ := l["codigo"].(float64); int(c) != http.StatusInternalServerError {
				t.Errorf("el pedido quedó anotado con el código %v", l["codigo"])
			}
			if l["ruta"] != "/docs/generar" {
				t.Errorf("la ruta quedó como %v", l["ruta"])
			}
			if l["metodo"] != http.MethodPost {
				t.Errorf("el método quedó como %v", l["metodo"])
			}
		case "pánico atendiendo un pedido":
			vioElPanico = true
			// Sin la traza la línea dice que algo se rompió y no dónde.
			if traza, _ := l["traza"].(string); !strings.Contains(traza, "goroutine") {
				t.Error("el pánico se registró sin traza")
			}
		}
	}
	if !vioElPedido {
		t.Error("el pedido no quedó en el log: un pánico lo hace desaparecer")
	}
	if !vioElPanico {
		t.Error("el pánico no quedó en el log")
	}
}
