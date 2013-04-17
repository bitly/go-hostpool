package hostpool

// --- Value Calculators -----------------

import (
	"math"
)

// --- Definitions -----------------------

type EpsilonValueCalculator interface {
	CalcValueFromAvgResponseTime(float64) float64
}

type LinearEpsilonValueCalculator struct{}
type LogEpsilonValueCalculator struct{ LinearEpsilonValueCalculator }
type PolynomialEpsilonValueCalculator struct {
	LinearEpsilonValueCalculator
	Exp float64 // the exponent to which we will raise the value to reweight
}

// -------- Methods -----------------------

func (c *LinearEpsilonValueCalculator) CalcValueFromAvgResponseTime(v float64) float64 {
	return 1.0 / v
}

func (c *LogEpsilonValueCalculator) CalcValueFromAvgResponseTime(v float64) float64 {
	return math.Log(c.LinearEpsilonValueCalculator.CalcValueFromAvgResponseTime(v))
}

func (c *PolynomialEpsilonValueCalculator) CalcValueFromAvgResponseTime(v float64) float64 {
	return math.Pow(c.LinearEpsilonValueCalculator.CalcValueFromAvgResponseTime(v), c.Exp)
}
