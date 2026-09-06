package mcp

// Herramientas describe lo que el modelo puede pedir. Las descripciones están
// escritas para que sepa cuándo usar cada una y qué esperar, no sólo qué
// argumentos toma.
//
// Es pública para que la documentación de la interfaz salga de esta misma
// lista y no de una copia que se desactualiza.
func Herramientas() []Herramienta {
	seccion := map[string]any{
		"type":        "string",
		"enum":        []string{"primera", "segunda", "tercera"},
		"description": "primera: decretos, resoluciones y disposiciones. segunda: sociedades, edictos y sucesiones. tercera: licitaciones y contrataciones.",
	}
	fecha := func(desc string) map[string]any {
		return map[string]any{"type": "string", "pattern": `^\d{4}-\d{2}-\d{2}$`, "description": desc}
	}

	hs := []Herramienta{
		{
			Nombre:      "edicion",
			Titulo:      "Edición de un día",
			Descripcion: "Devuelve el sumario del Boletín Oficial para una sección y una fecha: cuántos avisos hubo, cómo se reparten por rubro, y la lista con organismo, norma y síntesis de cada uno. Es el punto de partida para cualquier consulta sobre un día. Si ese día no hubo edición lo dice; no todos los días hay.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"seccion": seccion,
					"fecha":   fecha("Día a consultar, en AAAA-MM-DD. Si se omite, hoy."),
					"rubro":   map[string]any{"type": "string", "description": "Filtra por rubro, por nombre exacto o por su comienzo. Por ejemplo DECRETOS."},
					"limite":  map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "default": 40, "description": "Cuántos avisos traer. Una edición puede tener cientos."},
				},
				"required": []string{"seccion"},
			},
		},
		{
			Nombre:      "aviso",
			Titulo:      "Texto completo de un aviso",
			Descripcion: "Devuelve un aviso entero: su texto completo en texto plano y la lista de anexos con la URL para bajar cada PDF. El id y la fecha salen de la herramienta edicion o de buscar.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"seccion": seccion,
					"id":      map[string]any{"type": "string", "description": "Identificador del aviso. En la primera sección es numérico; en la segunda y la tercera puede ser alfanumérico, como A1522579."},
					"fecha":   fecha("Fecha de publicación del aviso, en AAAA-MM-DD."),
				},
				"required": []string{"seccion", "id", "fecha"},
			},
		},
		{
			Nombre:      "buscar",
			Titulo:      "Buscar avisos",
			Descripcion: "Busca avisos por texto dentro de un rango de fechas. Sirve para preguntas del tipo qué se publicó sobre un tema, o si un organismo dictó algo en un período. Devuelve el sumario de cada aviso; para leer el texto hay que pedir el aviso.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"seccion": seccion,
					"texto":   map[string]any{"type": "string", "description": "Palabras a buscar. No hace falta poner los acentos."},
					"desde":   fecha("Comienzo del rango, en AAAA-MM-DD."),
					"hasta":   fecha("Fin del rango, en AAAA-MM-DD."),
					"rubro":   map[string]any{"type": "string", "description": "Restringe a un rubro."},
					"pagina":  map[string]any{"type": "integer", "minimum": 1, "default": 1},
					"limite":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50},
				},
				"required": []string{"seccion", "desde", "hasta"},
			},
		},
		{
			Nombre:      "calendario",
			Titulo:      "Días con edición",
			Descripcion: "Devuelve qué días de un año tuvieron edición de una sección, y cuáles tuvieron suplemento. Conviene mirarlo antes de recorrer un rango de fechas: los feriados no tienen edición.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"seccion": seccion,
					"anio":    map[string]any{"type": "integer", "minimum": 1990, "description": "Año a consultar. Si se omite, el corriente."},
				},
				"required": []string{"seccion"},
			},
		},
		{
			Nombre:      "rubros",
			Titulo:      "Catálogo de rubros",
			Descripcion: "Devuelve los rubros con los que se clasifican los avisos de una sección. Sirve para saber por qué valor filtrar en edicion o en buscar.",
			Esquema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"seccion": seccion},
				"required":   []string{"seccion"},
			},
		},
		{
			Nombre:      "estado",
			Titulo:      "Estado del servicio",
			Descripcion: "Dice qué día es hoy en Argentina, si esta instancia tiene índice local de búsqueda y cuánta historia tiene guardada. Útil para saber qué se puede responder sin depender del sitio del Boletín.",
			Esquema:     map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
	hs = append(hs, herramientasProvinciales()...)
	hs = append(hs, herramientasNacionales()...)
	return append(hs, herramientasDeBusqueda()...)
}
