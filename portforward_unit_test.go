package ilmari

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPortForward_URL(t *testing.T) {
	pf := &PortForward{localPort: 12345}
	assert.Equal(t, "http://localhost:12345/health", pf.URL("/health"))
	assert.Equal(t, "http://localhost:12345/", pf.URL("/"))
	assert.Equal(t, "http://localhost:12345/api/v1/items", pf.URL("/api/v1/items"))
}

func TestPortForward_Close_NilChannels(t *testing.T) {
	pf := &PortForward{}
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
