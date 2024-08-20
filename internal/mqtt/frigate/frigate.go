package frigate

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
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

func cleanString(str string) string {
	strArr := []string{}
	for _, word := range strings.Split(str, "_") {
		strArr = append(strArr, cases.Title(language.English).String(word))
	}
	return strings.Join(strArr, " ")
}

func joinStringSlice(str []string) string {
	strArr := []string{}
	for _, s := range str {
		strArr = append(strArr, cleanString(s))
	}
	return strings.Join(strArr, " and ")
}

// Create mqtt Listeners from a config
func (d *Device) Listeners(config *config.Config) ([]listener, error) {
	_, listeners, err := listeners(config, nil)
	return listeners, err
}
func listeners(config *config.Config, client mqtt.Client) (*base, []listener, error) {
	listeners := []listener{}
	base := base{}
	for _, d := range config.Devices {
		if d.Type != "frigate" {
			continue
		}
		listenerConfig := listenerConfig{}
		listener := listener{
			Base: base,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		if err := yaml.Unmarshal(yamlConfig, &listenerConfig); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		if listenerConfig.Name == "" || listenerConfig.Timeout == 0 || listenerConfig.MQTT.Host == "" || listenerConfig.Frigate.URL == "" || listenerConfig.Alert.URL == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		if listenerConfig.MQTT.Port == 0 {
			listenerConfig.MQTT.Port = 1883
		}

		if listenerConfig.Frigate.ExternalUrl == "" {
			listenerConfig.Frigate.ExternalUrl = listenerConfig.Frigate.URL
		}

		if client == nil {
			clientOpts := mqtt.NewClientOptions()
			clientOpts.AddBroker(fmt.Sprintf("tcp://%s:%d", listenerConfig.MQTT.Host, listenerConfig.MQTT.Port))
			clientOpts.SetClientID("restate-go")
			client = mqtt.NewClient(clientOpts)
			token := client.Connect()
			if err = mqtt.WaitTokenTimeout(token, time.Duration(listenerConfig.Timeout)*time.Millisecond); err != nil {
				logging.Log(logging.Info, err.Error())
				continue
			}
		}
		listenerConfig.Client = client

		listener.Config = &listenerConfig
		base.Listeners = append(base.Listeners, &listener)
		listeners = append(listeners, listener)

		logging.Log(logging.Info, "Setup device \"%s\"", listener.Config.Name)
	}

	if len(listeners) == 0 {
		return nil, []listener{}, errors.New("no listeners found in config")
	}

	return &base, listeners, nil
}

// Subscribe to frigate reviews topic and process review messages.
func (l *listener) Listen() {
	if l.Config.Client == nil {
		logging.Log(logging.Error, "MQTT client is not initialized")
		return
	}

	token := l.Config.Client.Subscribe("frigate/reviews", 0, func(client mqtt.Client, message mqtt.Message) {
		review := review{}
		if err := json.Unmarshal(message.Payload(), &review); err != nil {
			logging.Log(logging.Error, "Failed to unmarshal MQTT message: %v", err)
			return
		}

		if !((review.Type == "new" && review.After.Severity == "alert") ||
			(review.Type == "update" && review.Before.Severity == "detection" && review.After.Severity == "alert")) {
			return
		}

		// Process the event and create alert request
		alertRequest := l.createAlertRequest(&review)

		_, _, _ = l.sendAlert(alertRequest)

	})
	// Avoid having to mock token in unit tests
	if token == nil {
		return
	}
	// Check that subscription to topic occured
	if err := mqtt.WaitTokenTimeout(token, time.Duration(l.Config.Timeout)*time.Millisecond); err != nil {
		logging.Log(logging.Error, "Failed to subscribe to MQTT topic: %v", token.Error())
	}
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
	message := fmt.Sprintf("%s detected at %s", joinStringSlice(review.After.Data.Objects), joinStringSlice(review.After.Data.Zones))
	// Obtain the event ID with the latest timestamp in the review
	eventIds := review.After.Data.Detections
	sort.Sort(sort.Reverse(sort.StringSlice(eventIds)))
	// Obtain associated thumbnail of the latest event ID
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
