package hostpool

import (
	"log"
	"math"
	"math/rand"
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

type epsilonHostPoolResponse struct {
	standardHostPoolResponse
	started time.Time
	ended   time.Time
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
	sync.Locker
}

type standardHostPool struct {
	sync.RWMutex
	hosts             map[string]HostEntry
	hostList          []HostEntry
	initialRetryDelay time.Duration
	maxRetryInterval  time.Duration
	nextHostIndex     int
}

type epsilonGreedyHostPool struct {
	HostPool
	epsilon                float32 // this is our exploration factor
	decayDuration          time.Duration
	EpsilonValueCalculator // embed the epsilonValueCalculator
	timer
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

// ------ constants -------------------

const epsilonBuckets = 120
const epsilonDecay = 0.90 // decay the exploration rate
const minEpsilon = 0.01   // explore one percent of the time
const initialEpsilon = 0.3
const defaultDecayDuration = time.Duration(5) * time.Minute

func New(hosts []string) HostPool {
	p := &standardHostPool{
		hosts:             make(map[string]HostEntry, len(hosts)),
		hostList:          make([]HostEntry, len(hosts)),
		initialRetryDelay: time.Duration(30) * time.Second,
		maxRetryInterval:  time.Duration(900) * time.Second,
	}

	for i, h := range hosts {
		e := newHostEntry(h, p.initialRetryDelay, p.maxRetryInterval)
		p.hosts[h] = e
		p.hostList[i] = e
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

// Epsilon Greedy is an algorithim that allows HostPool not only to track failure state, 
// but also to learn about "better" options in terms of speed, and to pick from available hosts
// based on a percentage of how well they perform. This gives a weighted request rate to better
// performing hosts, while still distributing requests to all hosts (proportionate to their performance)
// 
// After enabling Epsilon Greedy, hosts must be marked for sucess along with a time value representing 
// how fast (or slow) that host was. 
// 
// host := pool.Get()
// start := time.Now()
// ..... do work with host
// duration = time.Now().Sub(start)
// pool.MarkSuccessWithTime(host, duration)
// 
// a good overview of Epsilon Greedy is here http://stevehanov.ca/blog/index.php?id=132
//
// decayDuration may be set to 0 to use the default value of 5 minutes
func NewEpsilonGreedy(hosts []string, decayDuration time.Duration, calc EpsilonValueCalculator) HostPool {

	if decayDuration <= 0 {
		decayDuration = defaultDecayDuration
	}
	// stdHP := New(hosts).(*standardHostPool)
	p := &epsilonGreedyHostPool{
		HostPool:               New(hosts),
		epsilon:                float32(initialEpsilon),
		decayDuration:          decayDuration,
		EpsilonValueCalculator: calc,
		timer:                  &realTimer{},
	}

	// allocate structures
	for _, h := range p.hostList {
		h.epsilonCounts = make([]int64, epsilonBuckets)
		h.epsilonValues = make([]int64, epsilonBuckets)
	}
	go p.epsilonGreedyDecay()
	return p
}

func (rt *realTimer) between(start time.Time, end time.Time) time.Duration {
	return end.Sub(start)
}

func (p *epsilonGreedyHostPool) SetEpsilon(newEpsilon float32) {
	p.Lock()
	defer p.Unlock()
	p.epsilon = newEpsilon
}

func (p *epsilonGreedyHostPool) epsilonGreedyDecay() {
	durationPerBucket := p.decayDuration / epsilonBuckets
	ticker := time.Tick(durationPerBucket)
	for {
		<-ticker
		p.performEpsilonGreedyDecay()
	}
}
func (p *epsilonGreedyHostPool) performEpsilonGreedyDecay() {
	p.Lock()
	for _, h := range p.hostList {
		h.epsilonIndex += 1
		h.epsilonIndex = h.epsilonIndex % epsilonBuckets
		h.epsilonCounts[h.epsilonIndex] = 0
		h.epsilonValues[h.epsilonIndex] = 0
	}
	p.Unlock()
}

// return an upstream entry from the HostPool
func (p *standardHostPool) Get() HostPoolResponse {
	p.Lock()
	defer p.Unlock()
	host := p.getRoundRobin()
	return &standardHostPoolResponse{host: host, pool: p}
}

func (p *epsilonGreedyHostPool) Get() HostPoolResponse {
	p.Lock()
	defer p.Unlock()
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
	hostCount := len(p.hostList)
	for i := range p.hostList {
		// iterate via sequenece from where we last iterated
		currentIndex := (i + p.nextHostIndex) % hostCount

		h := p.hostList[currentIndex]
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
	return p.hostList[0].Host()
}

func (p *epsilonGreedyHostPool) getEpsilonGreedy() string {
	var hostToUse *hostEntry

	// this is our exploration phase
	if rand.Float32() < p.epsilon {
		p.epsilon = p.epsilon * epsilonDecay
		if p.epsilon < minEpsilon {
			p.epsilon = minEpsilon
		}
		return p.HostPool.Get().Host()
	}

	// calculate values for each host in the 0..1 range (but not ormalized)
	var possibleHosts []*hostEntry
	now := time.Now()
	var sumValues float64
	for _, h := range p.hostList {
		if h.canTryHost(now) {
			v := h.getWeightedAverageResponseTime()
			if v > 0 {
				ev := p.CalcValueFromAvgResponseTime(v)
				h.epsilonValue = ev
				sumValues += ev
				possibleHosts = append(possibleHosts, h)
			}
		}
	}

	if len(possibleHosts) != 0 {
		// now normalize to the 0..1 range to get a percentage
		for _, h := range possibleHosts {
			h.epsilonPercentage = h.epsilonValue / sumValues
		}

		// do a weighted random choice among hosts
		ceiling := 0.0
		pickPercentage := rand.Float64()
		for _, h := range possibleHosts {
			ceiling += h.epsilonPercentage
			if pickPercentage <= ceiling {
				hostToUse = h
				break
			}
		}
	}

	if hostToUse == nil {
		if len(possibleHosts) != 0 {
			log.Println("Failed to randomly choose a host, Dan loses")
		}
		return p.HostPool.Get().Host()
	}

	if hostToUse.dead {
		hostToUse.willRetryHost()
	}
	return hostToUse.host
}

func (h *hostEntry) getWeightedAverageResponseTime() float64 {
	var value float64
	var lastValue float64

	// start at 1 so we start with the oldest entry
	for i := 1; i <= epsilonBuckets; i += 1 {
		pos := (h.epsilonIndex + i) % epsilonBuckets
		bucketCount := h.epsilonCounts[pos]
		// Changing the line below to what I think it should be to get the weights right
		weight := float64(i) / float64(epsilonBuckets)
		if bucketCount > 0 {
			currentValue := float64(h.epsilonValues[pos]) / float64(bucketCount)
			value += currentValue * weight
			lastValue = currentValue
		} else {
			value += lastValue * weight
		}
	}
	return value
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

func (p *epsilonGreedyHostPool) markSuccess(hostR HostPoolResponse) {
	// first do the base markSuccess - a little redundant with host lookup but cleaner than repeating logic
	p.HostPool.markSuccess(hostR)
	eHostR, ok := hostR.(*epsilonHostPoolResponse)
	if !ok {
		log.Printf("Incorrect type in eps markSuccess!") // TODO reflection to print out offending type
		return
	}
	host := eHostR.host
	duration := p.between(eHostR.started, eHostR.ended)

	p.Lock()
	defer p.Unlock()
	h := p.lookupHost(host)
	h.epsilonCounts[h.epsilonIndex]++
	h.epsilonValues[h.epsilonIndex] += int64(duration.Seconds() * 1000)
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
	hosts := make([]string, len(p.hosts))
	for host, _ := range p.hosts {
		hosts = append(hosts, host)
	}
	return hosts
}

func (p *standardHostPool) lookupHost(hostname string) HostEntry {
	// We can do a "simple" lookup here because this map doesn't change once init'd
	h, ok := p.hosts[host]
	if !ok {
		log.Fatalf("host %s not in HostPool %v", host, p.Hosts())
	}
	return h
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
