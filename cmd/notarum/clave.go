package main

import (
	"os"
	"path/filepath"
)

// archivoClaveInicial es donde queda la clave generada de la cuenta que
// administra, dentro del directorio de datos.
//
// Antes se imprimía en el log, y los registros los ve cualquiera con acceso al
// panel del despliegue (y quedan guardados): una clave de administración no
// puede ir ahí. El archivo queda en el volumen, legible sólo por notarum, y se
// lee desde la consola del servicio.
const archivoClaveInicial = "clave-admin-inicial"

// guardarClaveInicial escribe la clave en dir con permisos 0600 y devuelve la
// ruta. Si el directorio no existe o no se puede escribir, devuelve el error y
// no deja nada a medias.
func guardarClaveInicial(dir, clave string) (string, error) {
	ruta := filepath.Join(dir, archivoClaveInicial)
	if err := os.WriteFile(ruta, []byte(clave+"\n"), 0o600); err != nil {
		return "", err
	}
	// WriteFile no cambia los permisos de un archivo que ya existía.
	if err := os.Chmod(ruta, 0o600); err != nil {
		return "", err
	}
	return ruta, nil
}

// avisoClaveGenerada arma los pares clave/valor del aviso de log. Nunca lleva
// la clave: sólo dónde leerla o, si no se pudo guardar, cómo entrar igual.
func avisoClaveGenerada(usuario, ruta string, errGuardar error) []any {
	if errGuardar != nil {
		return []any{
			"usuario", usuario,
			"no_se_pudo_guardar", errGuardar.Error(),
			"para_entrar", "poné NOTARUM_ADMIN_CLAVE en la configuración y reiniciá: esa pasa a ser la clave",
		}
	}
	return []any{
		"usuario", usuario,
		"leerla", "desde la consola del servicio: cat " + ruta,
		"despues", "borrá el archivo, o poné NOTARUM_ADMIN_CLAVE en la configuración",
	}
}
