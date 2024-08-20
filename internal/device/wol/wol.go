package wol

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"
	router "github.com/kennedn/restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"gopkg.in/yaml.v3"
)

type wol struct {
	Name       string `yaml:"name"`
	Timeout    uint   `yaml:"timeoutMs"`
	Host       string `yaml:"host"`
	MacAddress string `yaml:"macAddress"`
	base       base
	conn       net.PacketConn
}

type base struct {
	devices []*wol
	udpAddr *net.UDPAddr
}

type Device struct{}

func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config)
	return routes, err
}

func routes(config *config.Config) (*base, []router.Route, error) {
	routes := []router.Route{}

	base := base{
		udpAddr: &net.UDPAddr{
			IP:   net.ParseIP("192.168.1.255"),
			Port: 9,
		},
	}

	for _, d := range config.Devices {
		if d.Type != "wol" {
			continue
		}
		wol := wol{
			base: base,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		if err := yaml.Unmarshal(yamlConfig, &wol); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		if wol.Name == "" || wol.Host == "" || wol.MacAddress == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		routes = append(routes, router.Route{
			Path:    "/wol/" + wol.Name,
			Handler: wol.handler,
		})

		base.devices = append(base.devices, &wol)

		logging.Log(logging.Info, "Found device \"%s\"", wol.Name)
	}

	if len(routes) == 0 {
		return nil, []router.Route{}, errors.New("no routes generated from config")
	} else if len(routes) == 1 {
		return &base, routes, nil
	}

	routes = append(routes, router.Route{
		Path:    "/wol",
		Handler: base.handler,
	})

	routes = append(routes, router.Route{
		Path:    "/wol/",
		Handler: base.handler,
	})

	return &base, routes, nil
}

func (b *base) getDeviceNames() []string {
	var names []string
	for _, d := range b.devices {
		names = append(names, d.Name)
	}
	return names
}

func (b *base) handler(writer http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() {
		device.JSONResponse(writer, httpCode, jsonResponse)
	}()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", b.getDeviceNames())
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
	return
}

func (w *wol) wakeOnLan() error {
	var conn net.PacketConn
	var err error

	if w.conn != nil {
		conn = w.conn
	} else {
		conn, err = net.ListenPacket("udp", ":0")
		if err != nil {
			return err
		}
	}

	macAddress, err := net.ParseMAC(w.MacAddress)
	if err != nil {
		return err
	}

	if len(macAddress) != 6 {
		return errors.New("Invalid hardware address")
	}

	// 6 * 0xff (6 bytes) + 6 * macAddress (96 bytes) = 102
	payload := make([]byte, 102)

	// Set first 6 bytes to 0xFF
	for i := 0; i < 6; i++ {
		payload[i] = 0xFF
	}

	// Place 16 copies of macAddress (6 bytes) at offset i*6+6
	for i := 0; i < 16; i++ {
		copy(payload[i*6+6:i*6+12], macAddress)
	}

	conn.SetDeadline(time.Now().Add(time.Duration(w.Timeout) * time.Millisecond))
	_, err = conn.WriteTo(payload, w.base.udpAddr)
	return err
}

func (w *wol) ping() error {
	var conn net.PacketConn
	var err error

	if w.conn != nil {
		conn = w.conn
	} else {
		conn, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		if err != nil {
			return err
		}
	}
	defer func() {
		conn.Close()
	}()

	ipAddr, err := net.ResolveIPAddr("ip4", w.Host)
	if err != nil {
		return err
	}

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:  os.Getpid() & 0xffff,
			Seq: 1,
		},
	}

	msgBytes, err := msg.Marshal(nil)
	if err != nil {
		return err
	}

	conn.SetDeadline(time.Now().Add(time.Duration(w.Timeout) * time.Millisecond))
	_, err = conn.WriteTo(msgBytes, ipAddr)
	if err != nil {
		return err
	}

	response := make([]byte, 1500)
	_, _, err = conn.ReadFrom(response)
	if err != nil {
		return err
	}

	return nil
}

func (w *wol) handler(writer http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() {
		device.JSONResponse(writer, httpCode, jsonResponse)
	}()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", []string{"power", "status"})
		return
	}

	if r.Method != http.MethodPost {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	request := device.Request{}

	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Malformed Or Empty JSON Body", nil)
			return
		}
	} else {
		if err := schema.NewDecoder().Decode(&request, r.URL.Query()); err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Malformed or empty query string", nil)
			return
		}
	}

	switch request.Code {
	case "status":
		err := w.ping()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", "off")
			} else {
				httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			}
		} else {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", "on")
		}
		return

	case "power":
		err := w.wakeOnLan()
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
		return

	default:
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		return
	}
}
