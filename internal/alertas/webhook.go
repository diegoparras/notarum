package alertas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// El aviso a un webhook.
//
// La dirección la pone quien crea la alerta, y notarum es el que sale a
// buscarla: eso convierte al servicio en un mensajero de pedidos ajenos. Si no
// se controla a dónde, alguien con una cuenta puede usar a notarum para llegar
// a cosas que sólo son alcanzables desde adentro de la red donde corre —el
// panel de despliegue, la base de datos, los metadatos de la nube—. Por eso se
// rechazan las direcciones privadas, salvo que quien opera diga lo contrario
// para probar en su máquina.

// tiempoDeAviso es lo que se espera a un webhook. Corto a propósito: son
// muchos avisos seguidos y el que no contesta rápido no puede frenar al resto.
const tiempoDeAviso = 10 * time.Second

// permitirPrivadasVar deja apuntar a direcciones privadas. Es para probar en
// una máquina de desarrollo; en un servicio expuesto no corresponde.
const permitirPrivadasVar = "NOTARUM_WEBHOOK_PERMITE_PRIVADAS"

// ValidarWebhook revisa que la dirección se pueda usar.
func ValidarWebhook(crudo string) error {
	u, err := url.Parse(strings.TrimSpace(crudo))
	if err != nil {
		return errors.New("esa dirección no se entiende")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("la dirección tiene que empezar con http:// o https://")
	}
	if u.Host == "" {
		return errors.New("falta el servidor en la dirección")
	}
	if permitePrivadas() {
		return nil
	}
	if privada(u.Hostname()) {
		return errors.New("esa dirección es de una red interna, y notarum no manda avisos ahí: " +
			"poné una dirección pública")
	}
	return nil
}

func permitePrivadas() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(permitirPrivadasVar)))
	return v != "" && v != "0" && v != "no" && v != "false" && v != "off"
}

// privada dice si un nombre lleva a una dirección que no debería alcanzarse
// desde afuera. Resuelve el nombre: apuntar un dominio propio a 127.0.0.1 es
// la forma clásica de saltear un control que sólo mira el texto.
func privada(host string) bool {
	if host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ipPrivada(ip)
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		// Si no se puede resolver, no se puede afirmar que sea pública.
		return true
	}
	for _, ip := range ips {
		if ipPrivada(ip) {
			return true
		}
	}
	return false
}

func ipPrivada(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	// 100.64.0.0/10, el rango que usan las nubes para su red interna, y el
	// 169.254.169.254 de los metadatos ya entra por link-local.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}

// Aviso es lo que se manda al webhook.
type Aviso struct {
	Alerta    string         `json:"alerta"`
	AlertaID  string         `json:"alerta_id"`
	Fuente    Fuente         `json:"fuente"`
	Criterios Criterios      `json:"criterios"`
	Cuando    time.Time      `json:"cuando"`
	Total     int            `json:"total"`
	Novedades []Coincidencia `json:"novedades"`
	// Instancia es de dónde salió, para poder distinguir avisos de dos
	// notarum distintos que llegan al mismo lugar.
	Instancia string `json:"instancia,omitempty"`
}

// Mandar entrega el aviso.
func Mandar(ctx context.Context, cli *http.Client, destino string, a Aviso) error {
	if err := ValidarWebhook(destino); err != nil {
		return err
	}
	cuerpo, err := json.Marshal(a)
	if err != nil {
		return err
	}
	ctx, cancelar := context.WithTimeout(ctx, tiempoDeAviso)
	defer cancelar()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destino, bytes.NewReader(cuerpo))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "notarum (alertas)")

	res, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo avisar: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("el destino contestó %d", res.StatusCode)
	}
	return nil
}

// ClientePorDefecto es el que usa el servicio si no se le da otro. No sigue
// redirecciones: una redirección puede llevar de una dirección pública a una
// interna, que es justo lo que el control de arriba evita.
func ClientePorDefecto() *http.Client {
	return &http.Client{
		Timeout: tiempoDeAviso,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
