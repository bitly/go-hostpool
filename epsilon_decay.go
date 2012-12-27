package hostpool

import (
	"time"
)

// This will implement something that can, somewhat generically, keep track of 
// scores over time, with number of trials, so that we can do epsilon-greedy choice
// among options.

// Since this is being designed around response times, higher scores should be "worse"
// Not yet clear to me whether that detail will come into play at this level

const epsilonBuckets = 120
const defaultDecayDuration = time.Duration(5) * time.Minute

type EpsilonDecayStore interface {
	Record(score float64)
	GetWeightedAvgScore() float64
	performDecay() // this is only exposed in the interface for testing
}

type defEpsDecayStore struct {
	epsilonCounts []int64
	epsilonValues []float64
	epsilonIndex  int

	decayDuration time.Duration

	// incoming request channels
	recordReqChan     chan *recordRequest
	getWAScoreReqChan chan *getWAScoreRequest
}

type recordRequest struct {
	score    float64
	respChan chan struct{}
}

type getWAScoreRequest struct {
	respChan chan float64
}

// -- "Constructor" --

func NewDecayStore() EpsilonDecayStore {
	store := &defEpsDecayStore{
		epsilonCounts: make([]int64, epsilonBuckets),
		epsilonValues: make([]float64, epsilonBuckets),
		decayDuration: defaultDecayDuration,

		recordReqChan:     make(chan *recordRequest),
		getWAScoreReqChan: make(chan *getWAScoreRequest),
	}
	var numBuckets int64 = int64(len(store.epsilonCounts))
	durationPerBucket := time.Duration(int64(store.decayDuration) / numBuckets)
	ticker := time.Tick(durationPerBucket)
	go store.muxRequests(ticker)
	return store
}

// -- Public Methods --

func (ds *defEpsDecayStore) Record(score float64) {
	req := &recordRequest{
		score:    score,
		respChan: make(chan struct{}),
	}
	ds.recordReqChan <- req
	<-req.respChan
}

func (ds *defEpsDecayStore) GetWeightedAvgScore() float64 {
	req := &getWAScoreRequest{
		respChan: make(chan float64),
	}
	ds.getWAScoreReqChan <- req
	avgScore := <-req.respChan
	return avgScore
}

// -- Internal Methods --

func (ds *defEpsDecayStore) muxRequests(decayTicker <-chan time.Time) {
	for {
		select {
		case <-decayTicker:
			ds.performDecay()
		case req := <-ds.getWAScoreReqChan:
			avgScore := ds.getWeightedAverageScore()
			req.respChan <- avgScore
		case req := <-ds.recordReqChan:
			newScore := req.score
			ds.epsilonCounts[ds.epsilonIndex]++
			ds.epsilonValues[ds.epsilonIndex] += newScore
			req.respChan <- struct{}{}
		}

	}
}

// Methods below should only be called from muxRequests above

func (ds *defEpsDecayStore) performDecay() {
	ds.epsilonIndex += 1
	ds.epsilonIndex = ds.epsilonIndex % epsilonBuckets
	ds.epsilonCounts[ds.epsilonIndex] = 0
	ds.epsilonValues[ds.epsilonIndex] = 0.0
}

func (ds *defEpsDecayStore) getWeightedAverageScore() float64 {
	var value float64
	var lastValue float64

	// start at 1 so we start with the oldest entry
	for i := 1; i <= epsilonBuckets; i += 1 {
		pos := (ds.epsilonIndex + i) % epsilonBuckets
		bucketCount := ds.epsilonCounts[pos]
		weight := float64(i) / float64(epsilonBuckets)
		if bucketCount > 0 {
			currentValue := float64(ds.epsilonValues[pos]) / float64(bucketCount)
			value += currentValue * weight
			lastValue = currentValue
		} else {
			value += lastValue * weight
		}
	}
	return value
}
