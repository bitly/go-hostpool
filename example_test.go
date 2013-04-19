package hostpool_test

import (
	"github.com/bitly/go-hostpool"
)

func Example_epsilon_greedy() {
	hp := hostpool.NewEpsilonGreedy([]string{"a", "b"}, 0, &hostpool.LinearEpsilonValueCalculator{})
	hostResponse := hp.Get()
	hostname := hostResponse.Host()
	err := _ // (make a request with hostname)
	hostResponse.Mark(err)
}
