package ilmari

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPortForward_URL(t *testing.T) {
	pf := &PortForward{localPort: 12345}
	assert.Equal(t, "http://localhost:12345/health", pf.URL("/health"))
	assert.Equal(t, "http://localhost:12345/", pf.URL("/"))
	assert.Equal(t, "http://localhost:12345/api/v1/items", pf.URL("/api/v1/items"))
}

func TestPortForward_Get_WithError(t *testing.T) {
	pf := &PortForward{err: fmt.Errorf("connection failed")}
	resp, err := pf.Get("/health")
	assert.Nil(t, resp)
	assert.EqualError(t, err, "connection failed")
}

func TestPortForward_Post_WithError(t *testing.T) {
	pf := &PortForward{err: fmt.Errorf("connection failed")}
	resp, err := pf.Post("/api", "application/json", nil)
	assert.Nil(t, resp)
	assert.EqualError(t, err, "connection failed")
}

func TestPortForward_Put_WithError(t *testing.T) {
	pf := &PortForward{err: fmt.Errorf("connection failed")}
	resp, err := pf.Put("/api", "application/json", nil)
	assert.Nil(t, resp)
	assert.EqualError(t, err, "connection failed")
}

func TestPortForward_Delete_WithError(t *testing.T) {
	pf := &PortForward{err: fmt.Errorf("connection failed")}
	resp, err := pf.Delete("/api/item/1")
	assert.Nil(t, resp)
	assert.EqualError(t, err, "connection failed")
}

func TestPortForward_Do_WithError(t *testing.T) {
	pf := &PortForward{err: fmt.Errorf("connection failed")}
	resp, err := pf.Do(nil)
	assert.Nil(t, resp)
	assert.EqualError(t, err, "connection failed")
}

func TestPortForward_Close_NilChannels(t *testing.T) {
	pf := &PortForward{err: fmt.Errorf("init error")}
	// Should not panic
	pf.Close()
	// Double close should be safe
	pf.Close()
}

func TestPortForward_Close_WithChannels(t *testing.T) {
	stopChan := make(chan struct{}, 1)
	doneChan := make(chan struct{})
	close(doneChan) // Pre-close to simulate completed port forward

	pf := &PortForward{
		stopChan: stopChan,
		doneChan: doneChan,
	}
	// Should not panic
	pf.Close()
	// Double close should be safe
	pf.Close()
}
