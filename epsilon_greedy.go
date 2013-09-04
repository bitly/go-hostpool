package hostpool

import (
	"errors"
	"log"
	"math/rand"
	"sync"
	"time"
)

type epsilonHostPoolResponse struct {
	HostPoolResponse
	started  time.Time
	ended    time.Time
	selector *epsilonGreedySelector
}

func (r *epsilonHostPoolResponse) Mark(err error) {
	if err == nil {
		r.ended = time.Now()
		r.selector.recordTiming(r)
	}
	r.HostPoolResponse.Mark(err)
}

type epsilonGreedySelector struct {
	Selector
	sync.Locker
	epsilon                float32 // this is our exploration factor
	decayDuration          time.Duration
	EpsilonValueCalculator // embed the epsilonValueCalculator
	timer
}

// Construct an Epsilon Greedy Selector
//
// Epsilon Greedy is an algorithm that allows HostPool not only to track failure state, 
// but also to learn about "better" options in terms of speed, and to pick from available hosts
// based on how well they perform. This gives a weighted request rate to better
// performing hosts, while still distributing requests to all hosts (proportionate to their performance).
// The interface is the same as the standard HostPool, but be sure to mark the HostResponse immediately
// after executing the request to the host, as that will stop the implicitly running request timer.
// 
// A good overview of Epsilon Greedy is here http://stevehanov.ca/blog/index.php?id=132
//
// To compute the weighting scores, we perform a weighted average of recent response times, over the course of
// `decayDuration`. decayDuration may be set to 0 to use the default value of 5 minutes
// We then use the supplied EpsilonValueCalculator to calculate a score from that weighted average response time.
func NewEpsilonGreedy(decayDuration time.Duration, calc EpsilonValueCalculator) Selector {

	if decayDuration <= 0 {
		decayDuration = defaultDecayDuration
	}
	ss := &standardSelector{}
	s := &epsilonGreedySelector{
		Selector:               ss,
		Locker:                 ss,
		epsilon:                float32(initialEpsilon),
		decayDuration:          decayDuration,
		EpsilonValueCalculator: calc,
		timer:                  &realTimer{},
	}

	return s
}

func (s *epsilonGreedySelector) Init(hosts []string) {
	s.Selector.Init(hosts)
	// allocate structures
	for _, h := range s.Selector.(*standardSelector).hostList {
		h.epsilonCounts = make([]int64, epsilonBuckets)
		h.epsilonValues = make([]int64, epsilonBuckets)
	}
	go s.epsilonGreedyDecay()
}

func (s *epsilonGreedySelector) epsilonGreedyDecay() {
	durationPerBucket := s.decayDuration / epsilonBuckets
	ticker := time.Tick(durationPerBucket)
	for {
		<-ticker
		s.performEpsilonGreedyDecay()
	}
}
func (s *epsilonGreedySelector) performEpsilonGreedyDecay() {
	s.Lock()
	for _, h := range s.Selector.(*standardSelector).hostList {
		h.epsilonIndex += 1
		h.epsilonIndex = h.epsilonIndex % epsilonBuckets
		h.epsilonCounts[h.epsilonIndex] = 0
		h.epsilonValues[h.epsilonIndex] = 0
	}
	s.Unlock()
}

func (s *epsilonGreedySelector) SelectNextHost() string {
	s.Lock()
	host, err := s.getEpsilonGreedy()
	s.Unlock()
	if err != nil {
		host = s.Selector.SelectNextHost()
	}
	return host
}

func (s *epsilonGreedySelector) getEpsilonGreedy() (string, error) {
	var hostToUse *hostEntry

	// this is our exploration phase
	if rand.Float32() < s.epsilon {
		s.epsilon = s.epsilon * epsilonDecay
		if s.epsilon < minEpsilon {
			s.epsilon = minEpsilon
		}
		return "", errors.New("Exploration")
	}

	// calculate values for each host in the 0..1 range (but not ormalized)
	var possibleHosts []*hostEntry
	now := time.Now()
	var sumValues float64
	for _, h := range s.Selector.(*standardSelector).hostList {
		if h.canTryHost(now) {
			v := h.getWeightedAverageResponseTime()
			if v > 0 {
				ev := s.CalcValueFromAvgResponseTime(v)
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
		return "", errors.New("No host chosen")
	}
	return hostToUse.host, nil
}

func (s *epsilonGreedySelector) recordTiming(eHostR *epsilonHostPoolResponse) {
	host := eHostR.Host()
	duration := s.between(eHostR.started, eHostR.ended)

	s.Lock()
	defer s.Unlock()
	h, ok := s.Selector.(*standardSelector).hosts[host]
	if !ok {
		log.Fatalf("host %s not in HostPool", host)
	}
	h.epsilonCounts[h.epsilonIndex]++
	h.epsilonValues[h.epsilonIndex] += int64(duration.Seconds() * 1000)
}

func (s *epsilonGreedySelector) MakeHostResponse(host string) HostPoolResponse {
	resp := s.Selector.MakeHostResponse(host)
	return s.toEpsilonHostPoolResponse(resp)
}

// Convert regular response to one equipped for EG. Doesn't require lock, for now
func (s *epsilonGreedySelector) toEpsilonHostPoolResponse(resp HostPoolResponse) *epsilonHostPoolResponse {
	started := time.Now()
	return &epsilonHostPoolResponse{
		HostPoolResponse: resp,
		started:          started,
		selector:         s,
	}
}

// --- timer: this just exists for testing

type timer interface {
	between(time.Time, time.Time) time.Duration
}

type realTimer struct{}

func (rt *realTimer) between(start time.Time, end time.Time) time.Duration {
	return end.Sub(start)
}
