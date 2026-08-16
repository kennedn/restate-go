// matter-occupancy-light is an experimental Matter subscription consumer. It
// restores restate-go's existing controller material, subscribes to the
// Occupancy attribute, and mirrors changes to a Meross HTTP endpoint.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tom-code/gomat"
	"github.com/tom-code/gomat/mattertlv"
)

const (
	occupancyClusterID   = 0x0406
	occupancyAttributeID = 0x0000
	matterPort           = 5540
)

type state struct {
	FabricID     uint64 `json:"fabricId"`
	ControllerID uint64 `json:"controllerId"`
	NodeID       uint64 `json:"nodeId"`
	Commissioned bool   `json:"commissioned"`
}

type options struct {
	stateDir    string
	deviceName  string
	ip          string
	endpoint    uint
	minInterval uint
	maxInterval uint
	merossURL   string
}

func main() {
	var opts options
	flag.StringVar(&opts.stateDir, "state-dir", "", "exact persisted Matter device directory (auto-detected by -device when empty)")
	flag.StringVar(&opts.deviceName, "device", "office", "configured Matter device name used for state auto-detection")
	flag.StringVar(&opts.ip, "ip", "", "Matter device IPv4 address (required)")
	flag.UintVar(&opts.endpoint, "endpoint", 1, "Matter endpoint containing OccupancySensing")
	flag.UintVar(&opts.minInterval, "min-interval", 0, "subscription minimum reporting interval in seconds")
	flag.UintVar(&opts.maxInterval, "max-interval", 30, "subscription maximum reporting interval in seconds")
	flag.StringVar(&opts.merossURL, "meross-url", "https://api.kennedn.com/v2/meross/office", "Meross device URL")
	flag.Parse()

	if err := validateOptions(&opts); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for ctx.Err() == nil {
		err := runSubscription(ctx, opts)
		if ctx.Err() != nil {
			break
		}
		log.Printf("subscription ended: %v; reconnecting in 2s", err)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

func validateOptions(opts *options) error {
	parsedIP := net.ParseIP(opts.ip)
	if parsedIP == nil || parsedIP.To4() == nil {
		return fmt.Errorf("-ip must be a literal IPv4 address")
	}
	if opts.endpoint > 0xffff {
		return fmt.Errorf("-endpoint must fit in uint16")
	}
	if opts.minInterval > 0xffff || opts.maxInterval > 0xffff || opts.maxInterval < opts.minInterval {
		return fmt.Errorf("intervals must fit in uint16 and max-interval must be >= min-interval")
	}
	if opts.stateDir == "" {
		matches, err := filepath.Glob(filepath.Join("/tmp/state/matter", opts.deviceName+"-*"))
		if err != nil {
			return err
		}
		if len(matches) != 1 {
			return fmt.Errorf("expected one state directory matching %q, found %d; pass -state-dir explicitly", opts.deviceName+"-*", len(matches))
		}
		opts.stateDir = matches[0]
	}
	if _, err := url.ParseRequestURI(opts.merossURL); err != nil {
		return fmt.Errorf("invalid -meross-url: %w", err)
	}
	return nil
}

func runSubscription(ctx context.Context, opts options) error {
	persisted, fabric, restoreDirectory, err := restoreFabric(opts.stateDir)
	if err != nil {
		return err
	}
	defer restoreDirectory()

	plain, err := gomat.StartSecureChannel(net.ParseIP(opts.ip).To4(), matterPort, 0)
	if err != nil {
		return fmt.Errorf("start Matter UDP channel: %w", err)
	}
	channel, err := gomat.SigmaExchange(fabric, persisted.ControllerID, persisted.NodeID, plain)
	if err != nil {
		plain.Close()
		return fmt.Errorf("establish CASE: %w", err)
	}
	defer channel.Close()
	// gomat's Receive has its own timeout and does not accept a context. Closing
	// the packet connection makes SIGINT/SIGTERM interrupt an active receive now.
	stopReceiveInterrupt := make(chan struct{})
	defer close(stopReceiveInterrupt)
	go func() {
		select {
		case <-ctx.Done():
			_ = channel.Udp.Udp.Close()
		case <-stopReceiveInterrupt:
		}
	}()

	request := encodeAttributeSubscribeRequest(
		uint16(opts.endpoint), occupancyClusterID, occupancyAttributeID,
		uint16(opts.minInterval), uint16(opts.maxInterval),
	)
	if err := channel.Send(request); err != nil {
		return fmt.Errorf("send subscribe request: %w", err)
	}
	log.Printf("sent subscription request for endpoint %d OccupancySensing.Occupancy (min=%ds max=%ds)", opts.endpoint, opts.minInterval, opts.maxInterval)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	var previous uint64
	havePrevious := false
	initialReport := true
	setupDeadline := time.Now().Add(10 * time.Second)
	setupResponseSeen := false
	for ctx.Err() == nil {
		report, err := channel.Receive()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				if !setupResponseSeen && time.Now().After(setupDeadline) {
					return errors.New("device did not establish the subscription within 10 seconds")
				}
				continue
			}
			return fmt.Errorf("receive Matter subscription: %w", err)
		}
		if report.ProtocolHeader.ProtocolId == gomat.ProtocolIdSecureChannel && report.ProtocolHeader.Opcode == gomat.SEC_CHAN_OPCODE_STATUS_REP {
			return fmt.Errorf("Matter status report: general=%d protocol=%d code=%d", report.StatusReport.GeneralCode, report.StatusReport.ProtocolId, report.StatusReport.ProtocolCode)
		}
		switch report.ProtocolHeader.Opcode {
		case gomat.INTERACTION_OPCODE_SUBSC_RSP:
			setupResponseSeen = true
			log.Printf("Matter subscription established")
		case gomat.INTERACTION_OPCODE_REPORT_DATA:
			setupResponseSeen = true
			initiatorFlag := byte(0)
			if initialReport {
				initiatorFlag = 1
				initialReport = false
			}
			if err := channel.Send(gomat.EncodeIMStatusResponse(report.ProtocolHeader.ExchangeId, initiatorFlag)); err != nil {
				return fmt.Errorf("acknowledge Matter report: %w", err)
			}
			occupancy, err := occupancyFromReport(report.Tlv, uint16(opts.endpoint))
			if err != nil {
				if !errors.Is(err, errNoOccupancyData) {
					log.Printf("ignoring malformed occupancy report: %v", err)
				}
				continue
			}
			occupancy &= 1 // Occupancy is a bitmap; bit 0 is the occupied state.
			if !havePrevious {
				if err := postOccupancy(ctx, httpClient, opts.merossURL, occupancy); err != nil {
					log.Printf("initial occupancy sync=%d: %v", occupancy, err)
					continue
				}
				previous, havePrevious = occupancy, true
				log.Printf("initial occupancy=%d; Meross state synchronized", occupancy)
				continue
			}
			if occupancy == previous {
				continue
			}
			previous = occupancy
			if err := postOccupancy(ctx, httpClient, opts.merossURL, occupancy); err != nil {
				log.Printf("mirror occupancy=%d: %v", occupancy, err)
				continue
			}
			log.Printf("occupancy changed to %d; Meross state updated", occupancy)
		case gomat.INTERACTION_OPCODE_STATUS_RSP:
			// Status responses are expected during subscription setup/acknowledgment.
		default:
			log.Printf("ignoring Matter opcode 0x%x", report.ProtocolHeader.Opcode)
		}
	}
	return ctx.Err()
}

func restoreFabric(stateDir string) (state, *gomat.Fabric, func(), error) {
	raw, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil {
		return state{}, nil, func() {}, err
	}
	var persisted state
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return state{}, nil, func() {}, err
	}
	if !persisted.Commissioned || persisted.FabricID == 0 || persisted.ControllerID == 0 || persisted.NodeID == 0 {
		return state{}, nil, func() {}, errors.New("persisted Matter state is not commissioned or is incomplete")
	}

	oldDirectory, err := os.Getwd()
	if err != nil {
		return state{}, nil, func() {}, err
	}
	if err := os.Chdir(stateDir); err != nil {
		return state{}, nil, func() {}, err
	}
	restore := func() { _ = os.Chdir(oldDirectory) }
	manager := gomat.NewFileCertManager(persisted.FabricID)
	if err := manager.Load(); err != nil {
		restore()
		return state{}, nil, func() {}, fmt.Errorf("load persisted Matter certificates: %w", err)
	}
	return persisted, gomat.NewFabric(persisted.FabricID, manager), restore, nil
}

// encodeAttributeSubscribeRequest fills SubscribeRequest.AttributeRequests (tag
// 3). gomat's exported subscription helper fills EventRequests (tag 4), so it
// cannot represent an attribute subscription.
func encodeAttributeSubscribeRequest(endpoint uint16, cluster, attribute uint32, minInterval, maxInterval uint16) []byte {
	var tlv mattertlv.TLVBuffer
	tlv.WriteAnonStruct()
	tlv.WriteBool(0, false) // KeepSubscriptions
	tlv.WriteUInt16(1, minInterval)
	tlv.WriteUInt16(2, maxInterval)
	tlv.WriteArray(3) // AttributeRequests
	tlv.WriteAnonList()
	tlv.WriteUInt16(2, endpoint)
	tlv.WriteUInt32(3, cluster)
	tlv.WriteUInt32(4, attribute)
	tlv.WriteStructEnd()
	tlv.WriteStructEnd()
	tlv.WriteBool(7, true) // FabricFiltered
	tlv.WriteUInt8(0xff, 10)
	tlv.WriteStructEnd()

	var message bytes.Buffer
	message.WriteByte(5) // Initiator + needs acknowledgment
	message.WriteByte(byte(gomat.INTERACTION_OPCODE_SUBSC_REQ))
	_ = binary.Write(&message, binary.LittleEndian, uint16(1))
	_ = binary.Write(&message, binary.LittleEndian, uint16(gomat.ProtocolIdInteraction))
	message.Write(tlv.Bytes())
	return message.Bytes()
}

var errNoOccupancyData = errors.New("report contains no Occupancy attribute data")

func occupancyFromReport(report mattertlv.TlvItem, endpoint uint16) (uint64, error) {
	reports := report.GetItemWithTag(1) // ReportData.AttributeReports
	if reports == nil {
		return 0, errNoOccupancyData
	}
	if reports.Type != mattertlv.TypeList {
		return 0, errors.New("AttributeReports is not an array")
	}
	for _, attributeReport := range reports.GetChild() {
		attributeData := attributeReport.GetItemWithTag(1) // AttributeReportIB.AttributeData
		if attributeData == nil {
			// AttributeStatus entries legitimately contain no value.
			continue
		}
		path := attributeData.GetItemWithTag(1)
		value := attributeData.GetItemWithTag(2)
		if path == nil || value == nil {
			continue
		}
		pathEndpoint := path.GetItemWithTag(2)
		pathCluster := path.GetItemWithTag(3)
		pathAttribute := path.GetItemWithTag(4)
		if pathEndpoint == nil || pathCluster == nil || pathAttribute == nil {
			continue
		}
		if pathEndpoint.GetUint64() != uint64(endpoint) ||
			pathCluster.GetUint64() != occupancyClusterID ||
			pathAttribute.GetUint64() != occupancyAttributeID {
			continue
		}
		switch value.Type {
		case mattertlv.TypeInt:
			return value.GetUint64(), nil
		case mattertlv.TypeBool:
			if value.GetBool() {
				return 1, nil
			}
			return 0, nil
		default:
			return 0, fmt.Errorf("Occupancy value has unexpected TLV type %d", value.Type)
		}
	}
	return 0, errNoOccupancyData
}

func postOccupancy(ctx context.Context, client *http.Client, endpoint string, occupancy uint64) error {
	target, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	query := target.Query()
	query.Set("code", "toggle")
	query.Set("value", fmt.Sprintf("%d", occupancy))
	target.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", response.Status)
	}
	return nil
}
