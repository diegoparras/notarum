package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/vencimientos"
)

// La agenda de vencimientos de ARCA. Se busca como el resto —por fechas,
// impuesto, texto o CUIT— y abajo muestra lo que ninguna agenda publica: qué
// fechas movió ARCA.

const (
	porPaginaVencimientos = 40
	// diasPorDefecto es la ventana sin filtros: lo que viene en el próximo
	// mes. Arrancar con la agenda entera, vencido incluido, no le sirve a
	// nadie de entrada.
	diasPorDefecto = 30
)

type datosVencimientos struct {
	comun
	Disponible bool
	Cargado    bool
	Estado     servicio.EstadoVencimientos

	Desde, Hasta string
	CUIT         string
	Terminacion  string
	Impuesto     string
	Texto        string
	SinFecha     bool
	// PorDefecto dice que no se pidió ningún período y se muestra el próximo
	// mes.
	PorDefecto bool

	Impuestos []vencimientos.ConteoImpuesto
	Filas     []filaVencimiento
	Total     int
	Error     string
	Cambios   []filaCambio

	Pagina              int
	Anterior, Siguiente string
}

// filaVencimiento es un vencimiento listo para dibujar. Los días que faltan
// se calculan acá: la plantilla no tiene aritmética.
type filaVencimiento struct {
	vencimientos.Registro
	FechaTexto string
	Faltan     string
	Urgencia   string // vencido, hoy, pronto, lejos
}

type filaCambio struct {
	vencimientos.Cambio
	Texto string
}

func (s *Sitio) verVencimientos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := datosVencimientos{
		comun:      s.baseCon(r, "", ""),
		Disponible: s.srv.VencimientosDisponible(),
		Estado:     s.srv.EstadoVencimientos(),
		Desde:      strings.TrimSpace(q.Get("desde")),
		Hasta:      strings.TrimSpace(q.Get("hasta")),
		CUIT:       strings.TrimSpace(q.Get("cuit")),
		Impuesto:   strings.TrimSpace(q.Get("impuesto")),
		Texto:      strings.TrimSpace(q.Get("texto")),
		SinFecha:   q.Get("sin_fecha") != "",
		Pagina:     1,
	}
	if d.Disponible {
		d.Cargado = s.srv.AgendaCargada()
	}
	if !d.Cargado {
		s.mostrar(w, r, "vencimientos", d, http.StatusOK)
		return
	}
	d.Impuestos = s.srv.ImpuestosDeVencimientos()

	// Lo que no se entiende se dice. Ignorar un CUIT mal escrito mostraría la
	// agenda de todos, y quien la mira creería que es la suya.
	var err error
	if d.Terminacion, err = vencimientos.TerminacionDeCUIT(d.CUIT); err != nil {
		d.Error = conMayuscula(err.Error()) + "."
	}
	desde, errD := vencimientos.ParsearFecha(d.Desde)
	hasta, errH := vencimientos.ParsearFecha(d.Hasta)
	if errD != nil {
		d.Error = conMayuscula(errD.Error()) + "."
	} else if errH != nil {
		d.Error = conMayuscula(errH.Error()) + "."
	}
	if d.Error != "" {
		s.mostrar(w, r, "vencimientos", d, http.StatusOK)
		return
	}
	if desde != "" && hasta != "" && hasta < desde {
		desde, hasta = hasta, desde // dos fechas al revés son un descuido
	}
	hoy := boletin.HoyEnArgentina().Time
	if desde == "" && hasta == "" && !d.SinFecha {
		desde = hoy.Format(vencimientos.FormatoISO)
		hasta = hoy.AddDate(0, 0, diasPorDefecto).Format(vencimientos.FormatoISO)
		d.PorDefecto = true
	}
	d.Desde, d.Hasta = desde, hasta
	if p, err := strconv.Atoi(q.Get("pagina")); err == nil && p > 1 {
		d.Pagina = p
	}

	res := s.srv.BuscarVencimientos(vencimientos.Consulta{
		Desde: desde, Hasta: hasta, Impuesto: d.Impuesto, Terminacion: d.Terminacion,
		Texto: d.Texto, SinFechaFija: d.SinFecha,
		Limite: porPaginaVencimientos, Desplazamiento: (d.Pagina - 1) * porPaginaVencimientos,
	})
	d.Total = res.Total
	d.Filas = filasDeVencimientos(res.Vencimientos, hoy)
	d.Anterior, d.Siguiente = d.enlacesDePagina(res.Truncado)

	for _, c := range s.srv.CambiosDeVencimientos(time.Now().AddDate(0, 0, -90), 30) {
		d.Cambios = append(d.Cambios, filaCambio{Cambio: c, Texto: textoDeCambio(c)})
	}
	s.mostrar(w, r, "vencimientos", d, http.StatusOK)
}

func filasDeVencimientos(rs []vencimientos.Registro, hoy time.Time) []filaVencimiento {
	hoy = time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 0, 0, 0, 0, time.UTC)
	out := make([]filaVencimiento, 0, len(rs))
	for _, r := range rs {
		f := filaVencimiento{Registro: r, FechaTexto: "sin fecha fija", Faltan: "plazo relativo", Urgencia: "lejos"}
		if t, err := time.Parse(vencimientos.FormatoISO, r.Fecha); err == nil {
			f.FechaTexto = t.Format("02/01/2006")
			dias := int(t.Sub(hoy).Hours() / 24)
			switch {
			case dias < 0:
				f.Urgencia, f.Faltan = "vencido", fmt.Sprintf("hace %d día%s", -dias, plural(-dias))
			case dias == 0:
				f.Urgencia, f.Faltan = "hoy", "vence hoy"
			case dias <= 7:
				f.Urgencia, f.Faltan = "pronto", fmt.Sprintf("en %d día%s", dias, plural(dias))
			default:
				f.Faltan = fmt.Sprintf("en %d días", dias)
			}
		}
		out = append(out, f)
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// textoDeCambio dice un cambio en una línea.
func textoDeCambio(c vencimientos.Cambio) string {
	f := func(iso string) string {
		if t, err := time.Parse(vencimientos.FormatoISO, iso); err == nil {
			return t.Format("02/01/2006")
		}
		return iso
	}
	switch c.Tipo {
	case vencimientos.Prorroga:
		return fmt.Sprintf("se corrió del %s al %s (%d día%s más)", f(c.FechaAnterior), f(c.FechaNueva), c.Dias, plural(c.Dias))
	case vencimientos.Adelanto:
		return fmt.Sprintf("se adelantó del %s al %s (%d día%s antes)", f(c.FechaAnterior), f(c.FechaNueva), -c.Dias, plural(-c.Dias))
	case vencimientos.Alta:
		if c.FechaNueva == "" {
			return "apareció, sin fecha fija"
		}
		return "apareció, vence el " + f(c.FechaNueva)
	case vencimientos.Baja:
		if c.FechaAnterior == "" {
			return "ARCA la dejó de publicar"
		}
		return "ARCA la dejó de publicar; vencía el " + f(c.FechaAnterior)
	case vencimientos.Reaparicion:
		return "ARCA la volvió a publicar"
	}
	return c.Tipo
}

// enlacesDePagina conserva los filtros al pasar de página.
func (d datosVencimientos) enlacesDePagina(hayMas bool) (anterior, siguiente string) {
	con := func(pagina int) string {
		v := url.Values{}
		if !d.PorDefecto {
			if d.Desde != "" {
				v.Set("desde", d.Desde)
			}
			if d.Hasta != "" {
				v.Set("hasta", d.Hasta)
			}
		}
		for k, val := range map[string]string{"cuit": d.CUIT, "impuesto": d.Impuesto, "texto": d.Texto} {
			if val != "" {
				v.Set(k, val)
			}
		}
		if d.SinFecha {
			v.Set("sin_fecha", "1")
		}
		if pagina > 1 {
			v.Set("pagina", strconv.Itoa(pagina))
		}
		if len(v) == 0 {
			return "/vencimientos"
		}
		return "/vencimientos?" + v.Encode()
	}
	if d.Pagina > 1 {
		anterior = con(d.Pagina - 1)
	}
	if hayMas {
		siguiente = con(d.Pagina + 1)
	}
	return anterior, siguiente
}
