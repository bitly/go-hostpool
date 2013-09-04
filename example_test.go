package hostpool

import (
	"errors"
)

func ExampleNewEpsilonGreedy() {
	hp := NewWithSelector([]string{"a", "b"}, NewEpsilonGreedy(0, &LinearEpsilonValueCalculator{}))
	hostResponse := hp.Get()
	hostname := hostResponse.Host()
	err := errors.New("I am your http error from " + hostname) // (make a request with hostname)
	hostResponse.Mark(err)
}
