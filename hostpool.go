// A Go package to intelligently and flexibly pool among multiple hosts from your Go application.
// Host selection can operate in round robin or epsilon greedy mode, and unresponsive hosts are
// avoided. A good overview of Epsilon Greedy is here http://stevehanov.ca/blog/index.php?id=132
package hostpool

import (
	"time"
)

// Returns current version
func Version() string {
	return "0.1"
}

// --- Response interfaces and structs ----

// This interface represents the response from HostPool. You can retrieve the
// hostname by calling Host(), and after making a request to the host you should
// call Mark with any error encountered, which will inform the HostPool issuing
// the HostPoolResponse of what happened to the request and allow it to update.
type HostPoolResponse interface {
	Host() string
	Mark(error)
}

type standardHostPoolResponse struct {
	host string
	ss   *standardSelector
}

// --- HostPool structs and interfaces ----

// This is the main HostPool interface. Structs implementing this interface
// allow you to Get a HostPoolResponse (which includes a hostname to use),
// get the list of all Hosts, and use ResetAll to reset state.
type HostPool interface {
	Get() HostPoolResponse
	ResetAll()
	Hosts() []string
}

type standardHostPool struct {
	hosts []string
	Selector
}

// ------ constants -------------------

const epsilonBuckets = 120
const epsilonDecay = 0.90 // decay the exploration rate
const minEpsilon = 0.01   // explore one percent of the time
const initialEpsilon = 0.3
const defaultDecayDuration = time.Duration(5) * time.Minute

// Construct a basic HostPool using the hostnames provided
func New(hosts []string) HostPool {
	return NewWithSelector(hosts, &standardSelector{})
}

func NewWithSelector(hosts []string, s Selector) HostPool {
	s.Init(hosts)
	return &standardHostPool{
		hosts,
		s,
	}
}

func (r *standardHostPoolResponse) Host() string {
	return r.host
}

func (r *standardHostPoolResponse) Mark(err error) {
	r.ss.MarkHost(r.host, err)
}

// return an entry from the HostPool
func (p *standardHostPool) Get() HostPoolResponse {
	host := p.SelectNextHost()
	return p.MakeHostResponse(host)
}

func (p *standardHostPool) Hosts() []string {
	return p.hosts
}
