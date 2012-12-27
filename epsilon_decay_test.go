package hostpool

import (
	"github.com/bmizerany/assert"
	"log"
	"testing"
)

func TestEDS(t *testing.T) {
	eds := NewDecayStore()
	eds.Record(1.5)
	assert.Equal(t, eds.GetWeightedAvgScore(), 1.5)
}
