package vencimientos

import (
	"sort"
	"time"
)

// Resumen es lo que cambió en una bajada.
type Resumen struct {
	Filas     int `json:"filas"`
	Altas     int `json:"altas"`
	Prorrogas int `json:"prorrogas"`
	Adelantos int `json:"adelantos"`
	Bajas     int `json:"bajas"`
	// CargaInicial es la primera bajada: todo es nuevo y nada es noticia, así
	// que no se registra como cambio.
	CargaInicial bool `json:"carga_inicial,omitempty"`
}

// Novedades cuenta los cambios.
func (r Resumen) Novedades() int { return r.Altas + r.Prorrogas + r.Adelantos + r.Bajas }

// Comparar cruza lo que había con lo que trajo una bajada nueva y devuelve el
// estado nuevo y lo que cambió.
//
// Es la única forma de saber qué movió ARCA: la página publica la agenda
// vigente y no dice qué cambió ni cuándo. Es una función pura —no guarda nada—
// para que el mismo cruce valga con cualquier almacén.
//
// Las bajas se dan sólo adentro de la ventana que se consultó: que un
// vencimiento de otro año no aparezca en esta consulta no dice nada de él. Los
// de plazo relativo se publican en toda consulta, así que ahí sí cuentan.
func Comparar(anterior []Registro, ag Agenda, ahora time.Time) ([]Registro, []Cambio, Resumen) {
	res := Resumen{Filas: len(ag.Vencimientos), CargaInicial: len(anterior) == 0}
	previos := make(map[string]Registro, len(anterior))
	for _, r := range anterior {
		previos[r.Clave] = r
	}

	var cambios []Cambio
	vistos := make(map[string]bool, len(ag.Vencimientos))
	nuevo := make([]Registro, 0, len(anterior)+len(ag.Vencimientos))

	for _, v := range ag.Vencimientos {
		clave := v.Clave()
		if vistos[clave] {
			// La página no repite claves (verificado sobre un año entero). Si
			// alguna vez lo hace, la primera manda.
			continue
		}
		vistos[clave] = true

		prev, estaba := previos[clave]
		if !estaba {
			res.Altas++
			nuevo = append(nuevo, Registro{
				Clave: clave, Vencimiento: v,
				FechaDesde: ahora, VistoPrimero: ahora, VistoUltimo: ahora,
			})
			if !res.CargaInicial {
				cambios = append(cambios, cambioDe(clave, Alta, "", v.Fecha, time.Time{}, ahora, v))
			}
			continue
		}

		r := prev
		r.Vencimiento, r.VistoUltimo = v, ahora
		if prev.RetiradoEn != nil {
			r.RetiradoEn = nil
			cambios = append(cambios, cambioDe(clave, Reaparicion, prev.Fecha, v.Fecha, prev.VistoUltimo, ahora, v))
		}
		if prev.Fecha != v.Fecha {
			r.FechaDesde = ahora
			// Una misma clave no puede pasar de tener fecha a no tenerla: la
			// terminación es parte de la identidad y la de una obligación sin
			// fecha es vacía. Sólo se registra el caso con dos fechas.
			if prev.Fecha != "" && v.Fecha != "" {
				tipo := Prorroga
				if v.Fecha < prev.Fecha {
					tipo = Adelanto
					res.Adelantos++
				} else {
					res.Prorrogas++
				}
				cambios = append(cambios, cambioDe(clave, tipo, prev.Fecha, v.Fecha, prev.VistoUltimo, ahora, v))
			}
		}
		nuevo = append(nuevo, r)
	}

	desde, hasta := ag.Desde.Format(FormatoISO), ag.Hasta.Format(FormatoISO)
	for _, prev := range anterior {
		if vistos[prev.Clave] {
			continue
		}
		r := prev
		adentro := prev.SinFechaFija || (prev.Fecha >= desde && prev.Fecha <= hasta)
		if prev.RetiradoEn == nil && adentro && !ag.Desde.IsZero() {
			t := ahora
			r.RetiradoEn = &t
			res.Bajas++
			cambios = append(cambios, cambioDe(prev.Clave, Baja, prev.Fecha, "", prev.VistoUltimo, ahora, prev.Vencimiento))
		}
		nuevo = append(nuevo, r)
	}

	Ordenar(nuevo)
	return nuevo, cambios, res
}

func cambioDe(clave, tipo, anterior, nueva string, vistoAnterior, ahora time.Time, v Vencimiento) Cambio {
	c := Cambio{
		Clave: clave, Tipo: tipo, FechaAnterior: anterior, FechaNueva: nueva,
		VistoAnterior: vistoAnterior, DetectadoEn: ahora,
		Impuesto: v.Impuesto, Regimen: v.Regimen, Obligacion: v.Obligacion,
		Periodo: v.Periodo, TerminacionCUIT: v.TerminacionCUIT,
	}
	if anterior != "" && nueva != "" {
		a, errA := time.Parse(FormatoISO, anterior)
		n, errN := time.Parse(FormatoISO, nueva)
		if errA == nil && errN == nil {
			c.Dias = int(n.Sub(a).Hours() / 24)
		}
	}
	return c
}

// Ordenar deja la agenda como se lee: por fecha, las sin fecha al final, y
// después por impuesto. El desempate por clave hace que dos consultas iguales
// devuelvan lo mismo en el mismo orden, que es lo que permite paginar.
func Ordenar(rs []Registro) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if (a.Fecha == "") != (b.Fecha == "") {
			return b.Fecha == ""
		}
		if a.Fecha != b.Fecha {
			return a.Fecha < b.Fecha
		}
		if a.Impuesto != b.Impuesto {
			return a.Impuesto < b.Impuesto
		}
		if a.Periodo != b.Periodo {
			return a.Periodo < b.Periodo
		}
		if a.TerminacionCUIT != b.TerminacionCUIT {
			return a.TerminacionCUIT < b.TerminacionCUIT
		}
		return a.Clave < b.Clave
	})
}
