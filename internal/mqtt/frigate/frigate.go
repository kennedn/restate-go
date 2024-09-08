package frigate

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	alert "github.com/kennedn/restate-go/internal/device/alert/common"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

// Event represents the structure of the event received from MQTT.
type review struct {
	Type   string `json:"type"`
	Before detail `json:"before"`
	After  detail `json:"after"`
}

// Detail represents the detailed information of the event.
type detail struct {
	ID        string   `json:"id"`
	Camera    string   `json:"camera"`
	StartTime float64  `json:"start_time"`
	EndTime   *float64 `json:"end_time,omitempty"`
	Severity  string   `json:"severity"`
	ThumbPath string   `json:"thumb_path"`
	Data      struct {
		Detections []string `json:"detections"`
		Objects    []string `json:"objects"`
		SubLabels  []string `json:"sub_labels"`
		Zones      []string `json:"zones"`
		Audio      []string `json:"audio"`
	} `json:"data"`
}

type event struct {
	Area               json.Number `json:"area"`
	Box                []float64   `json:"box"`
	Camera             string      `json:"camera"`
	Data               eventData   `json:"data"`
	DetectorType       string      `json:"detector_type"`
	EndTime            float64     `json:"end_time"`
	FalsePositive      bool        `json:"false_positive"`
	HasClip            bool        `json:"has_clip"`
	HasSnapshot        bool        `json:"has_snapshot"`
	ID                 string      `json:"id"`
	Label              string      `json:"label"`
	ModelHash          string      `json:"model_hash"`
	ModelType          string      `json:"model_type"`
	PlusID             *string     `json:"plus_id"`
	Ratio              json.Number `json:"ratio"`
	Region             []float64   `json:"region"`
	RetainIndefinitely bool        `json:"retain_indefinitely"`
	Score              *float64    `json:"score"`
	StartTime          float64     `json:"start_time"`
	SubLabel           *string     `json:"sub_label"`
	Thumbnail          string      `json:"thumbnail"`
	TopScore           *float64    `json:"top_score"`
	Zones              []string    `json:"zones"`
}

type eventData struct {
	Attributes []string  `json:"attributes"`
	Box        []float64 `json:"box"`
	Region     []float64 `json:"region"`
	Score      float64   `json:"score"`
	TopScore   float64   `json:"top_score"`
	Type       string    `json:"type"`
}

type rawResponse struct {
	Status int      `json:"status"`
	Errors []string `json:"errors,omitempty"`
}

// Device represents an MQTT device that listens to messages and triggers alerts.
type listener struct {
	Base   base
	Config *listenerConfig
}

// Config represents the configuration for the MQTT alert device.
type listenerConfig struct {
	Name    string `yaml:"name"`
	Client  mqtt.Client
	Timeout uint `yaml:"timeoutMs"`
	MQTT    struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"mqtt"`
	Alert struct {
		URL      string `yaml:"url"`
		Token    string `yaml:"token"`
		User     string `yaml:"user"`
		Priority int    `yaml:"priority"`
	} `yaml:"alert"`
	Frigate struct {
		URL         string `yaml:"url"`
		ExternalUrl string `yaml:"externalUrl"`
		CacheEvents bool   `yaml:"cacheEvents"`
		CachePath   string `yaml:"cachePath"`
	} `yaml:"frigate"`
}

type base struct {
	Listeners []*listener
}

type Device struct{}

// toJsonNumber converts a numeric value to a JSON number.
func toJsonNumber(value any) json.Number {
	return json.Number(fmt.Sprintf("%d", value))
}

func humanizeString(str string) string {
	strArr := []string{}
	for _, word := range strings.Split(str, "_") {
		strArr = append(strArr, cases.Title(language.English).String(word))
	}
	return strings.Join(strArr, " ")
}

func joinStringSlice(str []string, seperator string, humanize bool) string {
	strArr := []string{}
	for _, s := range str {
		if humanize {
			strArr = append(strArr, humanizeString(s))
		} else {
			strArr = append(strArr, s)
		}
	}
	return strings.Join(strArr, seperator)
}

// Create mqtt Listeners from a config
func (d *Device) Listeners(config *config.Config) ([]listener, error) {
	_, listeners, err := listeners(config, nil)
	return listeners, err
}

// listeners is a function that creates one or more MQTT listeners
// It returns the base object and a slice of listeners.
func listeners(config *config.Config, client mqtt.Client) (*base, []listener, error) {
	listeners := []listener{}
	base := base{}

	// Iterate through each device in the configuration
	for _, d := range config.Devices {
		if d.Type != "frigate" {
			continue
		}

		listenerConfig := listenerConfig{}
		listener := listener{
			Base: base,
		}

		// Marshal the device config to YAML
		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		// Unmarshal the YAML config into the listenerConfig struct
		if err := yaml.Unmarshal(yamlConfig, &listenerConfig); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		// Check for missing parameters in the listenerConfig
		if listenerConfig.Name == "" || listenerConfig.Timeout == 0 || listenerConfig.MQTT.Host == "" || listenerConfig.Frigate.URL == "" || listenerConfig.Alert.URL == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		// Set default values for optional parameters
		if listenerConfig.MQTT.Port == 0 {
			listenerConfig.MQTT.Port = 1883
		}
		if listenerConfig.Frigate.ExternalUrl == "" {
			listenerConfig.Frigate.ExternalUrl = listenerConfig.Frigate.URL
		}
		if listenerConfig.Frigate.CacheEvents && listenerConfig.Frigate.CachePath == "" {
			listenerConfig.Frigate.CachePath = "/tmp/cache"
		}

		// Create MQTT client if not provided
		if client == nil {
			clientOpts := mqtt.NewClientOptions()
			clientOpts.AddBroker(fmt.Sprintf("tcp://%s:%d", listenerConfig.MQTT.Host, listenerConfig.MQTT.Port))
			clientOpts.SetClientID("restate-go")
			client = mqtt.NewClient(clientOpts)
		}

		// Attempt to connect to the MQTT broker with a timeout
		token := client.Connect()
		if err = mqtt.WaitTokenTimeout(token, time.Duration(listenerConfig.Timeout)*time.Millisecond); err != nil {
			logging.Log(logging.Info, err.Error())
			continue
		}

		// Set the MQTT client in the listenerConfig
		listenerConfig.Client = client

		// Set the listenerConfig in the listener
		listener.Config = &listenerConfig

		// Append the listener to the base object and the listeners slice
		base.Listeners = append(base.Listeners, &listener)
		listeners = append(listeners, listener)

		logging.Log(logging.Info, "Setup device \"%s\"", listener.Config.Name)
	}

	// Check if any listeners were created
	if len(listeners) == 0 {
		return nil, []listener{}, errors.New("no listeners found in config")
	}

	return &base, listeners, nil
}

func (l *listener) subscriptionCallback(_ mqtt.Client, message mqtt.Message) {
	review := review{}
	if err := json.Unmarshal(message.Payload(), &review); err != nil {
		logging.Log(logging.Error, "Failed to unmarshal MQTT message: %v", err)
		return
	}

	// Download a copy of each detection at the end of a given event for restic backup
	if l.Config.Frigate.CacheEvents && review.Type == "end" {
		// Download each detection in parallel
		var wg sync.WaitGroup
		// Parallel downloads can saturate IO, so create a ballpark timeout based on the number of detections to give downloads a chance to complete
		timeout := time.Duration(int(l.Config.Timeout)*(len(review.After.Data.Detections)+1)) * time.Millisecond
		for _, eventId := range review.After.Data.Detections {
			wg.Add(1)
			go func(eventId string) {
				defer wg.Done()
				err := l.downloadEvent(eventId, review.After.Severity, timeout)
				if err != nil {
					logging.Log(logging.Error, "Failed to cache event %s: %v", eventId, err)
				} else {
					logging.Log(logging.Info, "Cached event %s", eventId)
				}
			}(eventId)

		}
		wg.Wait()
		// Remove clips that no longer have an assosiated event in frigate
		err := l.removeOldClips()
		if err != nil {
			logging.Log(logging.Error, "Failed to remove old clips: %v", err)
		}
		return
	}

	// Return if this is not a new alert or an upgrade from detection to alert
	if !((review.Type == "new" && review.After.Severity == "alert") ||
		(review.Type == "update" && review.Before.Severity == "detection" && review.After.Severity == "alert")) {
		return
	}

	// Process the event and create alert request
	alertRequest := l.createAlertRequest(&review)

	_, _, _ = l.sendAlert(alertRequest)
}

// Subscribe to frigate reviews topic and process review messages.
func (l *listener) Listen() {
	if l.Config.Client == nil {
		logging.Log(logging.Error, "MQTT client is not initialized")
		return
	}

	// Configure callback for frigate reviews topic
	token := l.Config.Client.Subscribe("frigate/reviews", 0, l.subscriptionCallback)

	// Check that subscription to topic occured
	if err := mqtt.WaitTokenTimeout(token, time.Duration(l.Config.Timeout)*time.Millisecond); err != nil {
		logging.Log(logging.Error, "Failed to subscribe to MQTT topic: %v", token.Error())
	}
}

// Remove old clips that no longer have an associated event in frigate
func (l *listener) removeOldClips() error {
	// Retrieve all events currently in frigate database
	url := fmt.Sprintf("%s/api/events?limit=-1", l.Config.Frigate.URL)
	client := &http.Client{
		Timeout: time.Duration(l.Config.Timeout) * time.Millisecond,
	}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to get events: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get events: received status code %d", resp.StatusCode)
	}

	// Unmarshal events
	var events []event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return fmt.Errorf("failed to unmarshal events: %w", err)
	}

	// Create empty map of eventIdMap for quick lookup
	eventIdMap := make(map[string]struct{})
	for _, evt := range events {
		eventIdMap[evt.ID] = struct{}{}
	}

	// List all files in the cache directory
	files, err := os.ReadDir(l.Config.Frigate.CachePath)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	// Extract event IDs from filenames and compare with the event IDs from the endpoint
	for _, file := range files {

		if file.IsDir() {
			continue
		}

		filename := file.Name()

		// Check for .mp4 suffix
		if !strings.HasSuffix(filename, ".mp4") {
			continue
		}

		// Event ID should be part of the filename and separated by underscores
		splitFilename := strings.Split(filename, "_")
		if len(splitFilename) == 0 {
			continue
		}

		eventID := splitFilename[len(splitFilename)-1]
		eventID = strings.TrimSuffix(eventID, filepath.Ext(eventID))
		if _, exists := eventIdMap[eventID]; exists {
			continue
		}

		// Remove the file if the event ID no longer exists
		filePath := fmt.Sprintf("%s/%s", l.Config.Frigate.CachePath, filename)
		if err := os.Remove(filePath); err != nil {
			logging.Log(logging.Error, "Failed to remove file %s: %v", filePath, err)
		} else {
			logging.Log(logging.Info, "Removed file %s", filePath)
		}
	}

	return nil
}

// Generate a unique filename from a frigate event and download the associated clip
func (l *listener) downloadEvent(eventId string, severity string, timeout time.Duration) error {
	// Obtain metadata of event to build filename
	url := fmt.Sprintf("%s/api/events/%s", l.Config.Frigate.URL, eventId)
	client := &http.Client{
		Timeout: timeout,
	}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to get event: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get event: received status code %d", resp.StatusCode)
	}

	var evt event
	if err := json.NewDecoder(resp.Body).Decode(&evt); err != nil {
		return fmt.Errorf("failed to unmarshal event: %w", err)
	}

	// Must immediatly close the body here as we will be reusing the client
	resp.Body.Close()

	// Generate unique human readable filename using event metadata
	filename := fmt.Sprintf("%s_%s_%s_%s_%s.mp4",
		time.Unix(int64(evt.StartTime), 0).Format(time.RFC3339),
		severity,
		evt.Label,
		joinStringSlice(evt.Zones, "_", false),
		eventId,
	)

	url = fmt.Sprintf("%s/api/events/%s/clip.mp4", l.Config.Frigate.URL, eventId)

	resp, err = client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download event: received status code %d", resp.StatusCode)
	}

	// Create the file and write the response body to it
	file, err := os.Create(fmt.Sprintf("%s/%s", l.Config.Frigate.CachePath, filename))
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	return nil
}

// GET request to obtain the associated thumbnail image of a frigate eventID
func (l *listener) attachmentBase64(eventId string) (string, error) {
	method := "GET"
	url := fmt.Sprintf("%s/api/events/%s/thumbnail.jpg", l.Config.Frigate.URL, eventId)
	client := &http.Client{
		Timeout: time.Duration(l.Config.Timeout) * time.Millisecond,
	}

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch image: status code %d", resp.StatusCode)
	}

	// Read the image data from the response body
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Encode the image data to base64
	base64Image := base64.StdEncoding.EncodeToString(imageData)

	logging.NginxLog(logging.Info, method, url, req, resp)
	return base64Image, nil
}

// Generates a pushover alert request from a MQTT review message.
func (l *listener) createAlertRequest(review *review) alert.Request {
	// Create a message based on event details
	message := fmt.Sprintf("%s detected at %s",
		joinStringSlice(review.After.Data.Objects, " and ", true),
		joinStringSlice(review.After.Data.Zones, " and ", true))
	// Obtain the event ID with the latest timestamp in the review
	eventIds := review.After.Data.Detections
	sort.Sort(sort.Reverse(sort.StringSlice(eventIds)))
	// Obtain associated thumbnail of the latest event ID based on timestamp
	attachmentBase64, _ := l.attachmentBase64(eventIds[0])
	attachmentType := ""
	if attachmentBase64 != "" {
		attachmentType = "image/jpeg"
	}
	return alert.Request{
		Message:          message,
		Title:            "Frigate",
		Priority:         toJsonNumber(l.Config.Alert.Priority),
		Token:            l.Config.Alert.Token,
		User:             l.Config.Alert.User,
		URL:              l.Config.Frigate.ExternalUrl,
		URLTitle:         "Open Frigate",
		AttachmentBase64: attachmentBase64,
		AttachmentType:   attachmentType,
	}
}

// sendAlert sends a pushover alert based on the provided request.
func (l *listener) sendAlert(request alert.Request) (*rawResponse, int, error) {
	method := "POST"
	client := &http.Client{
		Timeout: time.Duration(l.Config.Timeout) * time.Millisecond,
	}

	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequest(method, l.Config.Alert.URL, bytes.NewReader(requestBytes))
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	rawResponse := rawResponse{}
	if err := json.Unmarshal(body, &rawResponse); err != nil {
		return nil, resp.StatusCode, err
	}

	logging.NginxLog(logging.Info, method, l.Config.Alert.URL, req, resp)
	return &rawResponse, resp.StatusCode, nil
}
