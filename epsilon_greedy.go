package hostpool

import (
	"log"
	"math/rand"
	"time"
)

type epsilonGreedyHostEntry interface {
	HostEntry
	EpsilonDecayStore
}

type epsilonGreedyHostPool struct {
	HostPool
	hosts                  map[string]epsilonGreedyHostEntry // this basically just mirrors the underlying host map
	epsilon                float32                           // this is our exploration factor
	EpsilonValueCalculator                                   // embed the epsilonValueCalculator
	timer
}

type epsilonHostPoolResponse struct {
	standardHostPoolResponse
	started time.Time
	ended   time.Time
}

// ------ constants -------------------

const epsilonDecay = 0.90 // decay the exploration rate
const minEpsilon = 0.01   // explore one percent of the time
const initialEpsilon = 0.3

func toEGHostEntry(fromHE HostEntry) epsilonGreedyHostEntry {
	return &struct {
		HostEntry
		EpsilonDecayStore
	}{
		fromHE,
		NewDecayStore(),
	}
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
	return ToEpsilonGreedy(New(hosts), decayDuration, calc)
}

func ToEpsilonGreedy(pool HostPool, decayDuration time.Duration, calc EpsilonValueCalculator) HostPool {
	if decayDuration <= 0 {
		decayDuration = defaultDecayDuration
	}
	p := &epsilonGreedyHostPool{
		HostPool:               pool,
		epsilon:                float32(initialEpsilon),
		EpsilonValueCalculator: calc,
		timer:                  &realTimer{},
	}

	p.hosts = make(map[string]epsilonGreedyHostEntry)
	for _, hostName := range pool.Hosts() {
		p.hosts[hostName] = toEGHostEntry(pool.lookupHost(hostName))
	}
	return p
}

// ---------

func (p *epsilonGreedyHostPool) getEpsilonGreedy() string {

	// this is our exploration phase
	if rand.Float32() < p.epsilon {
		p.epsilon = p.epsilon * epsilonDecay
		if p.epsilon < minEpsilon {
			p.epsilon = minEpsilon
		}
		return p.HostPool.Get().Host()
	}

	// calculate values for each host in the 0..1 range (but not ormalized)
	type epsilonChoice struct {
		he                HostEntry
		epsilonValue      float64
		epsilonPercentage float64
	}
	var hostToUse *epsilonChoice
	var possibleHosts []*epsilonChoice
	now := time.Now()
	var sumValues float64
	for _, h := range p.hosts {
		ec := &epsilonChoice{he: h}
		if h.canTryHost(now) {
			v := h.GetWeightedAvgScore() // score is the response time
			if v > 0 {
				ev := p.CalcValueFromAvgResponseTime(v)
				ec.epsilonValue = ev
				sumValues += ev
				possibleHosts = append(possibleHosts, ec)
			}
		}
	}

	if len(possibleHosts) != 0 {
		// now normalize to the 0..1 range to get a percentage
		for _, ec := range possibleHosts {
			ec.epsilonPercentage = ec.epsilonValue / sumValues
		}
		// do a weighted random choice among hosts
		ceiling := 0.0
		pickPercentage := rand.Float64()
		for _, ec := range possibleHosts {
			ceiling += ec.epsilonPercentage
			if pickPercentage <= ceiling {
				hostToUse = ec
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

	if hostToUse.he.IsDead() {
		hostToUse.he.willRetryHost()
	}
	return hostToUse.he.Host()
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

	h := p.hosts[host]
	h.Record(duration.Seconds())
}
