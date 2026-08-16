package ms600

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	router "github.com/kennedn/restate-go/internal/router/common"
	"github.com/tom-code/gomat"
	"github.com/tom-code/gomat/onboarding_payload"
	"gopkg.in/yaml.v3"
)

//go:embed device.yaml
var defaultInternalConfig []byte

type base struct {
	StateRoot  string `yaml:"stateRoot"`
	MatterPort int    `yaml:"matterPort"`
}

type matterConfig struct {
	Name     string `yaml:"name"`
	Timeout  uint   `yaml:"timeoutMs"`
	MatterQR string `yaml:"matterQR"`
	IP       string `yaml:"ip"`
	Port     int    `yaml:"-"`
}

type persistedState struct {
	Version      int    `json:"version"`
	FabricID     uint64 `json:"fabricId"`
	ControllerID uint64 `json:"controllerId"`
	NodeID       uint64 `json:"nodeId"`
	Commissioned bool   `json:"commissioned"`
}

type Device struct {
	clientFactory func(matterConfig, string, persistedState, uint32) (matterClient, error)
}

func (d *Device) Routes(cfg *config.Config) ([]router.Route, error) {
	_, routes, err := routes(cfg, nil, d.clientFactory)
	return routes, err
}

// routes mirrors the other Meross packages: it combines the public device
// declarations with an embedded internal device manifest. The override exists
// for fixture-driven tests, as it does in mts200b.
func routes(cfg *config.Config, internalConfigOverride *[]byte, clientFactory func(matterConfig, string, persistedState, uint32) (matterClient, error)) (*base, []router.Route, error) {
	internalConfig := defaultInternalConfig
	if internalConfigOverride != nil {
		internalConfig = *internalConfigOverride
	}
	base := &base{}
	if err := yaml.Unmarshal(internalConfig, base); err != nil {
		return nil, nil, fmt.Errorf("parse internal Matter config: %w", err)
	}
	if base.StateRoot == "" || base.MatterPort <= 0 || base.MatterPort > 65535 {
		return nil, nil, errors.New("unable to load internalConfig")
	}
	var routes []router.Route
	for _, configured := range cfg.Devices {
		if configured.Type != "matter" {
			continue
		}
		var mc matterConfig
		raw, err := yaml.Marshal(configured.Config)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal Matter config: %w", err)
		}
		if err := yaml.Unmarshal(raw, &mc); err != nil {
			return nil, nil, fmt.Errorf("parse Matter config: %w", err)
		}
		mc.Port = base.MatterPort
		deviceRoutes, err := routesForDevice(mc, base, clientFactory)
		if err != nil {
			return nil, nil, fmt.Errorf("Matter device %q: %w", mc.Name, err)
		}
		routes = append(routes, deviceRoutes...)
	}
	if len(routes) == 0 {
		return nil, nil, errors.New("no routes found in config")
	}
	return base, routes, nil
}

func routesForDevice(mc matterConfig, base *base, clientFactory func(matterConfig, string, persistedState, uint32) (matterClient, error)) ([]router.Route, error) {
	if mc.Name == "" || mc.IP == "" || mc.MatterQR == "" {
		return nil, errors.New("name, ip, and matterQR are required")
	}
	ip := net.ParseIP(mc.IP)
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("ip must be a literal IPv4 address, got %q", mc.IP)
	}
	qr, err := decodeQR(mc.MatterQR)
	if err != nil {
		return nil, err
	}
	directory := stateDirectory(base.StateRoot, mc.Name)
	state, exists, err := loadOrCreateState(directory)
	if err != nil {
		return nil, err
	}
	if clientFactory == nil {
		clientFactory = newGomatClient
	}
	client, err := clientFactory(mc, directory, state, qr.Passcode)
	if err != nil {
		return nil, err
	}
	if !exists || !state.Commissioned {
		logging.Log(logging.Info, "Commissioning Matter device %q at %s", mc.Name, mc.IP)
		if err := client.Commission(); err != nil {
			return nil, err
		}
		state.Commissioned = true
		if err := storeState(directory, state); err != nil {
			return nil, fmt.Errorf("persist commissioned Matter state: %w", err)
		}
	}
	if err := client.StartSession(); err != nil {
		return nil, err
	}
	model, err := discover(mc.Name, state.NodeID, client)
	if err != nil {
		client.CloseSession()
		return nil, fmt.Errorf("discover capabilities: %w", err)
	}
	targets := projectRoutes(model)
	routes := make([]router.Route, 0, len(targets)+1)
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		target := target
		path := "/" + mc.Name + "/" + target.Path
		paths = append(paths, target.Path)
		routes = append(routes, router.Route{Path: path, Handler: targetHandler(client, target)})
	}
	sort.Strings(paths)
	routes = append(routes, router.Route{Path: "/" + mc.Name, Handler: func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			respond(w, http.StatusMethodNotAllowed, "Method Not Allowed", nil)
			return
		}
		respond(w, http.StatusOK, "OK", paths)
	}})
	logging.Log(logging.Info, "Found Matter device %q with %d dynamic routes", mc.Name, len(targets))
	return routes, nil
}

func newGomatClient(mc matterConfig, directory string, state persistedState, pin uint32) (matterClient, error) {
	manager, err := newNamespacedCertManager(directory, state.FabricID)
	if err != nil {
		return nil, err
	}
	if state.Commissioned {
		err = manager.Load()
	} else {
		err = manager.BootstrapAndLoad()
	}
	if err != nil {
		return nil, fmt.Errorf("load Matter fabric credentials: %w", err)
	}
	if state.Commissioned {
		if err := validateControllerMaterial(directory, state.ControllerID); err != nil {
			return nil, err
		}
	}
	if !state.Commissioned {
		if err := manager.CreateUser(state.ControllerID); err != nil {
			return nil, fmt.Errorf("create Matter controller identity: %w", err)
		}
		if err := validateControllerMaterial(directory, state.ControllerID); err != nil {
			return nil, err
		}
	}
	fabric := gomat.NewFabric(state.FabricID, manager)
	return &gomatClient{ip: net.ParseIP(mc.IP).To4(), port: mc.Port, fabric: fabric, controllerID: state.ControllerID, nodeID: state.NodeID, pin: pin}, nil
}

func validateControllerMaterial(directory string, controllerID uint64) error {
	files := []string{
		filepath.Join(directory, "pem", "ca-private.pem"),
		filepath.Join(directory, "pem", "ca-cert.pem"),
		filepath.Join(directory, "pem", fmt.Sprintf("%d-private.pem", controllerID)),
		filepath.Join(directory, "pem", fmt.Sprintf("%d-cert.pem", controllerID)),
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("persisted Matter controller material is incomplete (%s): %w", path, err)
		}
		if info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("Matter private state has unsafe permissions %o on %s", info.Mode().Perm(), path)
		}
	}
	return nil
}

func decodeQR(value string) (content onboarding_payload.QrContent, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("invalid matterQR: %v", recovered)
		}
	}()
	if !strings.HasPrefix(value, "MT:") {
		return content, errors.New("matterQR must begin with MT:")
	}
	content = onboarding_payload.DecodeQrText(value)
	if content.Passcode == 0 {
		return content, errors.New("matterQR contains an invalid zero setup passcode")
	}
	return content, nil
}

func stateDirectory(stateRoot, name string) string {
	hash := sha256.Sum256([]byte(name))
	slug := kebab(name)
	if slug == "" {
		slug = "device"
	}
	return filepath.Join(stateRoot, fmt.Sprintf("%s-%x", slug, hash[:6]))
}

func loadOrCreateState(directory string) (persistedState, bool, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return persistedState{}, false, err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return persistedState{}, false, err
	}
	path := filepath.Join(directory, "state.json")
	raw, err := os.ReadFile(path)
	if err == nil {
		var state persistedState
		if err := json.Unmarshal(raw, &state); err != nil {
			return state, true, fmt.Errorf("parse %s: %w", path, err)
		}
		if state.Version != 1 || state.FabricID == 0 || state.ControllerID == 0 || state.NodeID == 0 {
			return state, true, fmt.Errorf("invalid persisted Matter state in %s", path)
		}
		return state, true, nil
	}
	if !os.IsNotExist(err) {
		return persistedState{}, false, err
	}
	state := persistedState{Version: 1, FabricID: randomID(), ControllerID: randomID(), NodeID: randomID()}
	if err := storeState(directory, state); err != nil {
		return state, false, err
	}
	return state, false, nil
}

func randomID() uint64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	id := binary.LittleEndian.Uint64(raw[:])
	if id == 0 {
		return 1
	}
	return id
}

func storeState(directory string, state persistedState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(directory, "state.json.tmp")
	if err := os.WriteFile(temporary, raw, 0600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(directory, "state.json"))
}
