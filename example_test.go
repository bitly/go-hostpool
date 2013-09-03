package hostpool

import (
	"errors"
)

func ExampleNewEpsilonGreedy() {
	hp := NewEpsilonGreedy([]string{"a", "b"}, 0, &LinearEpsilonValueCalculator{})
	hostResponse := Get(hp)
	hostname := hostResponse.Host()
	err := errors.New("I am your http error from " + hostname) // (make a request with hostname)
	hostResponse.Mark(err)
}
