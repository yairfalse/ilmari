package ilmari

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPatchType_Constants(t *testing.T) {
	// Verify patch type constants are defined correctly
	assert.Equal(t, PatchType("strategic"), PatchStrategic)
	assert.Equal(t, PatchType("merge"), PatchMerge)
	assert.Equal(t, PatchType("json"), PatchJSON)
}
