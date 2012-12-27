package hostpool

import (
	"log"
	"math"
	"sort"
	"sync"
	"time"
)

// --- timer: this just exists for testing

type timer interface {
	between(time.Time, time.Time) time.Duration
}

type realTimer struct{}

// --- Response interfaces and structs ----

type HostPoolResponse interface {
	Host() string
	Mark(error)
	hostPool() HostPool
}

type standardHostPoolResponse struct {
	host string
	sync.Once
	pool HostPool
}

// --- HostPool structs and interfaces ----

type HostPool interface {
	Get() HostPoolResponse
	// keep the marks separate so we can override independently
	markSuccess(HostPoolResponse)
	markFailed(HostPoolResponse)

	ResetAll()
	Hosts() []string
	lookupHost(string) HostEntry
	// setHostMap(map[string]HostEntry)
	sync.Locker
}

type standardHostPool struct {
	sync.RWMutex
	hosts             map[string]HostEntry
	initialRetryDelay time.Duration
	maxRetryInterval  time.Duration
	nextHostIndex     int
}

// --- Value Calculators -----------------

type EpsilonValueCalculator interface {
	CalcValueFromAvgResponseTime(float64) float64
}

type LinearEpsilonValueCalculator struct{}
type LogEpsilonValueCalculator struct{ LinearEpsilonValueCalculator }
type PolynomialEpsilonValueCalculator struct {
	LinearEpsilonValueCalculator
	exp float64 // the exponent to which we will raise the value to reweight
}

func New(hosts []string) HostPool {
	p := &standardHostPool{
		hosts:             make(map[string]HostEntry, len(hosts)),
		initialRetryDelay: time.Duration(30) * time.Second,
		maxRetryInterval:  time.Duration(900) * time.Second,
	}

	for _, h := range hosts {
		e := newHostEntry(h, p.initialRetryDelay, p.maxRetryInterval)
		p.hosts[h] = e
	}
	return p
}

func (r *standardHostPoolResponse) Host() string {
	return r.host
}

func (r *standardHostPoolResponse) hostPool() HostPool {
	return r.pool
}

func (r *standardHostPoolResponse) Mark(err error) {
	r.Do(func() {
		doMark(err, r)
	})
}

func doMark(err error, r HostPoolResponse) {
	if err == nil {
		r.hostPool().markSuccess(r)
	} else {
		r.hostPool().markFailed(r)
	}
}

func (r *epsilonHostPoolResponse) Mark(err error) {
	r.Do(func() {
		r.ended = time.Now()
		doMark(err, r)
	})

}

func (rt *realTimer) between(start time.Time, end time.Time) time.Duration {
	return end.Sub(start)
}

func (p *epsilonGreedyHostPool) SetEpsilon(newEpsilon float32) {
	p.Lock()
	defer p.Unlock()
	p.epsilon = newEpsilon
}

// return an upstream entry from the HostPool
func (p *standardHostPool) Get() HostPoolResponse {
	p.Lock()
	defer p.Unlock()
	host := p.getRoundRobin()
	return &standardHostPoolResponse{host: host, pool: p}
}

func (p *epsilonGreedyHostPool) Get() HostPoolResponse {
	host := p.getEpsilonGreedy()
	started := time.Now()
	return &epsilonHostPoolResponse{
		standardHostPoolResponse: standardHostPoolResponse{host: host, pool: p},
		started:                  started,
	}
}

func (p *standardHostPool) getRoundRobin() string {
	// TODO - will want to replace this with something that runs in a 
	// goroutine and receives requests on a channel.
	// The state being protected in that case is really just the currentIdx

	// Question - should I just skip the goroutine shit and select randomly?
	// Maybe
	now := time.Now()
	hostCount := len(p.hosts)
	for i := range p.hostList() {
		// iterate via sequenece from where we last iterated
		currentIndex := (i + p.nextHostIndex) % hostCount

		h := p.hostList()[currentIndex]
		if h.canTryHost(now) {
			if h.IsDead() {
				h.willRetryHost()
			}
			p.nextHostIndex = currentIndex + 1
			return h.Host()
		}
	}

	// all hosts are down. re-add them
	p.doResetAll()
	p.nextHostIndex = 0
	return p.hostList()[0].Host()
}

func (p *standardHostPool) ResetAll() {
	p.Lock()
	defer p.Unlock()
	p.doResetAll()
}

// this actually performs the logic to reset,
// and should only be called when the lock has
// already been acquired
func (p *standardHostPool) doResetAll() {
	for _, h := range p.hosts {
		h.SetDead(false)
	}
}

func (p *standardHostPool) markSuccess(hostR HostPoolResponse) {
	host := hostR.Host()
	p.Lock()
	defer p.Unlock()

	h, ok := p.hosts[host]
	if !ok {
		log.Fatalf("host %s not in HostPool %v", host, p.Hosts())
	}
	h.SetDead(false)
}

func (p *standardHostPool) markFailed(hostR HostPoolResponse) {
	host := hostR.Host()
	p.Lock()
	defer p.Unlock()
	h, ok := p.hosts[host]
	if !ok {
		log.Fatalf("host %s not in HostPool %v", host, p.Hosts())
	}
	h.SetDead(true)
}

func (p *standardHostPool) Hosts() []string {
	hosts := make([]string, 0, len(p.hosts))
	for host, _ := range p.hosts {
		hosts = append(hosts, host)
	}
	return hosts
}

func (p *standardHostPool) lookupHost(hostname string) HostEntry {
	// We can do a "simple" lookup here because this map doesn't change once init'd
	h, ok := p.hosts[hostname]
	if !ok {
		log.Fatalf("host %s not in HostPool %v", hostname, p.Hosts())
	}
	return h
}

func (p *standardHostPool) hostList() []HostEntry {
	// This returns a sorted list of HostEntry's. We ought
	// to do some optimization so that this isn't computed every time
	// TODO can totally use Hosts above to accomplish this
	keys := make([]string, 0, len(p.hosts))
	vals := make([]HostEntry, 0, len(p.hosts))
	for hostName := range p.hosts {
		keys = append(keys, hostName)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vals = append(vals, p.hosts[k])
	}
	return vals
}

// -------- Epsilon Value Calculators ----------

func (c *LinearEpsilonValueCalculator) CalcValueFromAvgResponseTime(v float64) float64 {
	return 1.0 / v
}

func (c *LogEpsilonValueCalculator) CalcValueFromAvgResponseTime(v float64) float64 {
	return math.Log(c.LinearEpsilonValueCalculator.CalcValueFromAvgResponseTime(v))
}

func (c *PolynomialEpsilonValueCalculator) CalcValueFromAvgResponseTime(v float64) float64 {
	return math.Pow(c.LinearEpsilonValueCalculator.CalcValueFromAvgResponseTime(v), c.exp)
}
