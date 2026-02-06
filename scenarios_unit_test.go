package ilmari

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTrafficConfig_Defaults(t *testing.T) {
	config := TrafficConfig{
		rps:      10,
		duration: 10 * time.Second,
		endpoint: "/",
	}
	assert.Equal(t, 10, config.rps)
	assert.Equal(t, 10*time.Second, config.duration)
	assert.Equal(t, "/", config.endpoint)
}

func TestTrafficConfig_RPS(t *testing.T) {
	config := &TrafficConfig{}
	config.RPS(100)
	assert.Equal(t, 100, config.rps)
}

func TestTrafficConfig_Duration(t *testing.T) {
	config := &TrafficConfig{}
	config.Duration(30 * time.Second)
	assert.Equal(t, 30*time.Second, config.duration)
}

func TestTrafficConfig_Endpoint(t *testing.T) {
	config := &TrafficConfig{}
	config.Endpoint("/api/v1/health")
	assert.Equal(t, "/api/v1/health", config.endpoint)
}

func TestTraffic_P99Latency_Empty(t *testing.T) {
	traffic := &Traffic{
		latencies: []time.Duration{},
	}
	assert.Equal(t, time.Duration(0), traffic.P99Latency())
}

func TestTraffic_P99Latency_SingleValue(t *testing.T) {
	traffic := &Traffic{
		latencies: []time.Duration{100 * time.Millisecond},
	}
	assert.Equal(t, 100*time.Millisecond, traffic.P99Latency())
}

func TestTraffic_P99Latency_MultipleValues(t *testing.T) {
	latencies := make([]time.Duration, 100)
	for i := 0; i < 100; i++ {
		latencies[i] = time.Duration(i+1) * time.Millisecond
	}
	traffic := &Traffic{
		latencies: latencies,
	}
	p99 := traffic.P99Latency()
	// 99th percentile of 1-100ms should be around 99ms or 100ms
	assert.True(t, p99 >= 99*time.Millisecond)
}

func TestTraffic_P99Latency_UnsortedInput(t *testing.T) {
	// P99Latency should sort internally, not require sorted input
	traffic := &Traffic{
		latencies: []time.Duration{
			500 * time.Millisecond,
			100 * time.Millisecond,
			300 * time.Millisecond,
			200 * time.Millisecond,
			400 * time.Millisecond,
		},
	}
	p99 := traffic.P99Latency()
	assert.Equal(t, 500*time.Millisecond, p99)
}

func TestTraffic_P99Latency_DoesNotMutateOriginal(t *testing.T) {
	original := []time.Duration{
		500 * time.Millisecond,
		100 * time.Millisecond,
		300 * time.Millisecond,
	}
	traffic := &Traffic{
		latencies: original,
	}
	_ = traffic.P99Latency()
	// Original should remain unsorted
	assert.False(t, sort.SliceIsSorted(original, func(i, j int) bool {
		return original[i] < original[j]
	}))
}

func TestTraffic_ErrorRate_NoRequests(t *testing.T) {
	traffic := &Traffic{}
	assert.Equal(t, float64(0), traffic.ErrorRate())
}

func TestTraffic_ErrorRate_AllSuccess(t *testing.T) {
	traffic := &Traffic{total: 100, errors: 0}
	assert.Equal(t, float64(0), traffic.ErrorRate())
}

func TestTraffic_ErrorRate_HalfErrors(t *testing.T) {
	traffic := &Traffic{total: 100, errors: 50}
	assert.InDelta(t, 0.5, traffic.ErrorRate(), 0.001)
}

func TestTraffic_TotalRequests(t *testing.T) {
	traffic := &Traffic{total: 42}
	assert.Equal(t, int64(42), traffic.TotalRequests())
}
