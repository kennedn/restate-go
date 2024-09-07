package mockPahoMqtt

import (
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Client struct {
	SubscribeFunc func(client mqtt.Client, callback mqtt.MessageHandler)
}

// IsConnected returns a hardcoded true value indicating the client is always connected
func (mc *Client) IsConnected() bool {
	return true
}

// IsConnectionOpen returns a hardcoded true value indicating the connection is always open
func (mc *Client) IsConnectionOpen() bool {
	return true
}

// Connect simulates a connection and returns a mock Token
func (mc *Client) Connect() mqtt.Token {
	return &Token{}
}

// Disconnect simulates a disconnect operation with no real effect
func (mc *Client) Disconnect(quiesce uint) {}

// Publish simulates publishing a message and returns a mock Token
func (mc *Client) Publish(topic string, qos byte, retained bool, payload interface{}) mqtt.Token {
	return &Token{}
}

// Subscribe simulates a subscription and returns a mock Token
func (mc *Client) Subscribe(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token {
	mc.SubscribeFunc(mc, callback)
	return &Token{}
}

// SubscribeMultiple simulates multiple subscriptions and returns a mock Token
func (mc *Client) SubscribeMultiple(filters map[string]byte, callback mqtt.MessageHandler) mqtt.Token {
	return &Token{}
}

// Unsubscribe simulates unsubscribing from topics and returns a mock Token
func (mc *Client) Unsubscribe(topics ...string) mqtt.Token {
	return &Token{}
}

// AddRoute simulates adding a route with a message handler
func (mc *Client) AddRoute(topic string, callback mqtt.MessageHandler) {}

// OptionsReader returns a nil ClientOptionsReader
func (mc *Client) OptionsReader() mqtt.ClientOptionsReader {
	return mqtt.ClientOptionsReader{}
}

// Message is a mock implementation of the Message interface
type Message struct {
	duplicate  bool
	qos        byte
	retained   bool
	topic      string
	messageID  uint16
	PayloadVar []byte
	ack        func()
	once       sync.Once
}

// Duplicate returns whether the message is a duplicate
func (m *Message) Duplicate() bool {
	return m.duplicate
}

// Qos returns the QoS level of the message
func (m *Message) Qos() byte {
	return m.qos
}

// Retained returns whether the message is retained
func (m *Message) Retained() bool {
	return m.retained
}

// Topic returns the topic of the message
func (m *Message) Topic() string {
	return m.topic
}

// MessageID returns the message ID
func (m *Message) MessageID() uint16 {
	return m.messageID
}

// Payload returns the payload of the message
func (m *Message) Payload() []byte {
	return m.PayloadVar
}

// Ack performs the acknowledgment callback
func (m *Message) Ack() {
	m.once.Do(m.ack)
}

// Token is a mock implementation of the Token interface
type Token struct {
}

// Wait mocks the Wait method
func (m *Token) Wait() bool {
	return true
}

// WaitTimeout mocks the WaitTimeout method
func (m *Token) WaitTimeout(timeout time.Duration) bool {
	return true
}

// Done mocks the Done method
func (m *Token) Done() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// Error mocks the Error method
func (m *Token) Error() error {
	return nil
}
