package alertas

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// La pasada: recorrer las alertas, ver qué apareció y avisar.
//
// Corre después de cada actualización de los catálogos, que es cuando puede
// haber algo nuevo. Una alerta que falla no frena a las demás: su error queda
// anotado en ella y se ve en la cuenta de quien la creó, que es quien puede
// arreglarlo.

// Buscador es lo que sabe mirar cada fuente. Lo cumple el servicio; está acá
// como interfaz para que las alertas no dependan de él.
type Buscador interface {
	BuscarParaAlerta(ctx context.Context, f Fuente, c Criterios) ([]Coincidencia, error)
}

// Corredor evalúa las alertas.
type Corredor struct {
	reg    *Registro
	buscar Buscador
	http   *http.Client
	// instancia es la dirección de este notarum, para que quien recibe el
	// aviso sepa de dónde salió.
	instancia string
}

func NuevoCorredor(reg *Registro, b Buscador, instancia string) *Corredor {
	return &Corredor{reg: reg, buscar: b, http: ClientePorDefecto(), instancia: instancia}
}

// ConCliente cambia con qué se mandan los avisos. Es para las pruebas.
func (c *Corredor) ConCliente(cli *http.Client) *Corredor {
	c.http = cli
	return c
}

// Resumen es lo que dejó una pasada.
type Resumen struct {
	Alertas   int `json:"alertas"`
	Corridas  int `json:"corridas"`
	Avisadas  int `json:"avisadas"`
	Novedades int `json:"novedades"`
	Fallaron  int `json:"fallaron"`
}

// Correr evalúa todas las alertas activas.
func (c *Corredor) Correr(ctx context.Context, avisar func(string)) (Resumen, error) {
	todas := c.reg.Todas()
	r := Resumen{Alertas: len(todas)}

	for i := range todas {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		a := todas[i]
		if !a.Activa {
			continue
		}
		if avisar != nil {
			avisar("mirando «" + a.Nombre + "»")
		}
		nuevas := c.correrUna(ctx, &a)
		r.Corridas++
		if a.Error != "" {
			r.Fallaron++
		}
		if len(nuevas) > 0 {
			r.Avisadas++
			r.Novedades += len(nuevas)
		}
		if err := c.reg.Actualizar(&a); err != nil {
			slog.Warn("no se pudo guardar el estado de una alerta", "alerta", a.ID, "err", err)
		}
	}
	return r, nil
}

// correrUna evalúa una alerta y deja en ella lo que pasó.
func (c *Corredor) correrUna(ctx context.Context, a *Alerta) []Coincidencia {
	primera := a.UltimaCorrida.IsZero()
	coincidencias, err := c.buscar.BuscarParaAlerta(ctx, a.Fuente, a.Criterios)
	if err != nil {
		// El error queda en la alerta y no en el log del servidor: quien puede
		// arreglarlo es quien la creó, y lo va a ver en su cuenta.
		a.Error = err.Error()
		a.UltimaCorrida = time.Now().UTC()
		return nil
	}
	a.Error = ""

	nuevas, vistos := a.Novedades(coincidencias)
	a.Vistos = vistos
	a.UltimaCorrida = time.Now().UTC()
	if len(nuevas) == 0 {
		if primera {
			slog.Info("alerta estrenada", "alerta", a.Nombre, "dueño", a.Dueño,
				"ya_habia", len(vistos))
		}
		return nil
	}

	a.UltimoAviso = time.Now().UTC()
	a.Avisados += len(nuevas)
	a.Ultimas = recortar(append(nuevas, a.Ultimas...), MaximasUltimas)

	if a.Webhook != "" {
		aviso := Aviso{
			Alerta: a.Nombre, AlertaID: a.ID, Fuente: a.Fuente,
			Criterios: a.Criterios, Cuando: a.UltimoAviso,
			Total: len(nuevas), Novedades: nuevas, Instancia: c.instancia,
		}
		if err := Mandar(ctx, c.http, a.Webhook, aviso); err != nil {
			// Lo encontrado no se pierde porque el aviso no haya salido: queda
			// en la alerta y se ve en la cuenta.
			a.Error = "no se pudo avisar al webhook: " + err.Error()
			slog.Warn("no se pudo avisar", "alerta", a.Nombre, "err", err)
		}
	}
	slog.Info("alerta con novedades", "alerta", a.Nombre, "dueño", a.Dueño,
		"novedades", len(nuevas), "webhook", a.Webhook != "")
	return nuevas
}

func recortar(cs []Coincidencia, tope int) []Coincidencia {
	if len(cs) <= tope {
		return cs
	}
	return cs[:tope]
}

// Probar corre una alerta sola, sin guardar nada ni avisar a nadie.
//
// Es para verla antes de guardarla: una alerta que no se puede probar se crea
// a ciegas y se descubre a la semana que no coincidía con nada.
func (c *Corredor) Probar(ctx context.Context, a Alerta) ([]Coincidencia, error) {
	return c.buscar.BuscarParaAlerta(ctx, a.Fuente, a.Criterios)
}
