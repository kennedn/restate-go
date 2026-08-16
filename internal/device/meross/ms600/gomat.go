package ms600

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/tom-code/gomat"
	"github.com/tom-code/gomat/mattertlv"
)

// Commissioning in gomat binds UDP/55555, so first-time commissioning remains
// serialized. Operational CASE sessions use ephemeral local ports per device.
var gomatSessionMu sync.Mutex
var workingDirectoryMu sync.Mutex

type matterClient interface {
	Commission() error
	StartSession() error
	CloseSession()
	Read(endpoint uint16, cluster, attribute uint32) (any, error)
	ReadIDs(endpoint uint16, cluster, attribute uint32) ([]uint32, error)
	Invoke(endpoint uint16, cluster, command uint32, payload []byte) error
}

type gomatClient struct {
	ip           net.IP
	port         int
	fabric       *gomat.Fabric
	controllerID uint64
	nodeID       uint64
	pin          uint32
	active       *gomat.SecureChannel
	mu           sync.Mutex
}

// namespacedCertManager adapts gomat's otherwise useful file manager, whose
// paths are hard-coded to ./pem, to a per-device state directory.
type namespacedCertManager struct {
	directory string
	inner     *gomat.FileCertManager
}

var _ gomat.CertificateManager = (*namespacedCertManager)(nil)

func newNamespacedCertManager(directory string, fabricID uint64) (*namespacedCertManager, error) {
	if err := os.MkdirAll(filepath.Join(directory, "pem"), 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	return &namespacedCertManager{directory: directory, inner: gomat.NewFileCertManager(fabricID)}, nil
}

func (m *namespacedCertManager) scoped(fn func() error) error {
	workingDirectoryMu.Lock()
	defer workingDirectoryMu.Unlock()
	old, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(m.directory); err != nil {
		return err
	}
	defer func() { _ = os.Chdir(old) }()
	return fn()
}

func (m *namespacedCertManager) BootstrapAndLoad() error {
	return m.scoped(func() error {
		if err := m.inner.BootstrapCa(); err != nil {
			return err
		}
		return m.inner.Load()
	})
}
func (m *namespacedCertManager) Load() error                     { return m.scoped(m.inner.Load) }
func (m *namespacedCertManager) GetCaPublicKey() ecdsa.PublicKey { return m.inner.GetCaPublicKey() }

// Explicit forwarding methods satisfy gomat.CertificateManager while applying
// the state namespace only to methods that touch disk.
func (m *namespacedCertManager) GetCaCertificate() *x509.Certificate {
	return m.inner.GetCaCertificate()
}
func (m *namespacedCertManager) GetCertificate(id uint64) (cert *x509.Certificate, err error) {
	err = m.scoped(func() error { cert, err = m.inner.GetCertificate(id); return err })
	return
}
func (m *namespacedCertManager) GetPrivkey(id uint64) (key *ecdsa.PrivateKey, err error) {
	err = m.scoped(func() error { key, err = m.inner.GetPrivkey(id); return err })
	return
}
func (m *namespacedCertManager) CreateUser(id uint64) (err error) {
	return m.scoped(func() error { return m.inner.CreateUser(id) })
}
func (m *namespacedCertManager) SignCertificate(key *ecdsa.PublicKey, id uint64) (cert *x509.Certificate, err error) {
	err = m.scoped(func() error { cert, err = m.inner.SignCertificate(key, id); return err })
	return
}

func (c *gomatClient) Commission() error {
	gomatSessionMu.Lock()
	defer gomatSessionMu.Unlock()
	if err := gomat.Commission(c.fabric, c.ip, int(c.pin), c.controllerID, c.nodeID); err != nil {
		return fmt.Errorf("Matter commissioning failed; open a commissioning window and retry: %w", err)
	}
	return nil
}

func (c *gomatClient) session(retry bool, fn func(*gomat.SecureChannel) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		channel, err := c.connect()
		if err != nil {
			return err
		}
		c.active = &channel
	}
	if err := fn(c.active); err != nil {
		c.closeSessionLocked()
		if !retry {
			return err
		}
		channel, connectErr := c.connect()
		if connectErr != nil {
			return fmt.Errorf("Matter exchange failed (%v), then reconnect failed: %w", err, connectErr)
		}
		c.active = &channel
		if retryErr := fn(c.active); retryErr != nil {
			c.closeSessionLocked()
			return retryErr
		}
	}
	return nil
}

// connect avoids gomat.ConnectDevice because that helper discards its bound UDP
// channel when Sigma/CASE fails, leaking the fixed local port until process exit.
func (c *gomatClient) connect() (gomat.SecureChannel, error) {
	plain, err := gomat.StartSecureChannel(c.ip, c.port, 0)
	if err != nil {
		return gomat.SecureChannel{}, fmt.Errorf("establish Matter CASE session: %w", err)
	}
	secure, err := gomat.SigmaExchange(c.fabric, c.controllerID, c.nodeID, plain)
	if err != nil {
		plain.Close()
		return gomat.SecureChannel{}, fmt.Errorf("establish Matter CASE session: %w", err)
	}
	return secure, nil
}

// StartSession pins one CASE channel for startup introspection. Matter discovery
// performs many reads and repeatedly negotiating CASE can exhaust device sessions.
func (c *gomatClient) StartSession() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != nil {
		return nil
	}
	channel, err := c.connect()
	if err != nil {
		return err
	}
	c.active = &channel
	return nil
}

func (c *gomatClient) CloseSession() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeSessionLocked()
}

func (c *gomatClient) closeSessionLocked() {
	if c.active != nil {
		c.active.Close()
		c.active = nil
	}
}

func (c *gomatClient) readItem(endpoint uint16, cluster, attribute uint32) (*mattertlv.TlvItem, error) {
	var item *mattertlv.TlvItem
	err := c.session(true, func(channel *gomat.SecureChannel) error {
		if err := channel.Send(gomat.EncodeIMReadRequest(endpoint, cluster, attribute)); err != nil {
			return err
		}
		response, err := channel.Receive()
		if err != nil {
			return err
		}
		if response.ProtocolHeader.Opcode != gomat.INTERACTION_OPCODE_REPORT_DATA {
			return fmt.Errorf("unexpected Matter read opcode 0x%x", response.ProtocolHeader.Opcode)
		}
		item = response.Tlv.GetItemRec([]int{1, 0, 1, 2})
		if item == nil {
			return errors.New("Matter read response contained no attribute data")
		}
		return nil
	})
	return item, err
}

func (c *gomatClient) Read(endpoint uint16, cluster, attribute uint32) (any, error) {
	item, err := c.readItem(endpoint, cluster, attribute)
	if err != nil {
		return nil, err
	}
	return tlvValue(*item), nil
}

func (c *gomatClient) ReadIDs(endpoint uint16, cluster, attribute uint32) ([]uint32, error) {
	item, err := c.readItem(endpoint, cluster, attribute)
	if err != nil {
		return nil, err
	}
	if item.Type != mattertlv.TypeList {
		return nil, fmt.Errorf("Matter attribute 0x%x/0x%x is not a list", cluster, attribute)
	}
	ids := make([]uint32, 0, len(item.GetChild()))
	for _, child := range item.GetChild() {
		ids = append(ids, uint32(child.GetUint64()))
	}
	return ids, nil
}

func (c *gomatClient) Invoke(endpoint uint16, cluster, command uint32, payload []byte) error {
	// Commands are not retried: a lost response does not prove the device failed
	// to execute the command. The failed session is still discarded for next use.
	return c.session(false, func(channel *gomat.SecureChannel) error {
		request := gomat.EncodeIMInvokeRequest(endpoint, cluster, command, payload, false, uint16(command))
		if err := channel.Send(request); err != nil {
			return err
		}
		response, err := channel.Receive()
		if err != nil {
			return err
		}
		if response.ProtocolHeader.Opcode != gomat.INTERACTION_OPCODE_INVOKE_RSP {
			return fmt.Errorf("unexpected Matter invoke opcode 0x%x", response.ProtocolHeader.Opcode)
		}
		status := gomat.ParseImInvokeResponse(&response.Tlv)
		// InvokeResponseIB is either a status or command data. gomat's helper only
		// parses the former, so accept a well-formed command-data response too.
		if status == -1 && response.Tlv.GetItemRec([]int{1, 0, 0}) != nil {
			return nil
		}
		if status != 0 {
			return fmt.Errorf("Matter command returned status %d", status)
		}
		return nil
	})
}

func tlvValue(item mattertlv.TlvItem) any {
	switch item.Type {
	case mattertlv.TypeNull:
		return nil
	case mattertlv.TypeBool:
		return item.GetBool()
	case mattertlv.TypeInt:
		return item.GetUint64()
	case mattertlv.TypeUTF8String:
		return item.GetString()
	case mattertlv.TypeOctetString:
		return hex.EncodeToString(item.GetOctetString())
	case mattertlv.TypeList:
		values := make([]any, 0, len(item.GetChild()))
		for _, child := range item.GetChild() {
			values = append(values, tlvValue(child))
		}
		return values
	default:
		return nil
	}
}
