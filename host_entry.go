package hostpool

import (
	"math"
	"time"
)

type HostEntry interface {
	IsDead() bool
	Host() string
	SetDead(bool)
	canTryHost(time.Time) bool
	willRetryHost()
}

// -- Requests

type hostEntryRequest interface {
	getRespChan() chan<- interface{}
}

type baseHostEntryRequest struct {
	respChan chan interface{}
}

func (req *baseHostEntryRequest) getRespChan() chan<- interface{} {
	return req.respChan
}

type isDeadRequest struct{ baseHostEntryRequest }

type setDeadRequest struct {
	baseHostEntryRequest
	setDeadTo bool
}

type canTryRequest struct {
	baseHostEntryRequest
	atTime time.Time
}

type willRetryRequest struct{ baseHostEntryRequest }

type hostEntry struct {
	host              string
	nextRetry         time.Time
	retryDelay        time.Duration
	initialRetryDelay time.Duration
	maxRetryInterval  time.Duration
	dead              bool
	// epsilonCounts     []int64
	// epsilonValues     []int64
	// epsilonIndex      int
	// epsilonValue      float64
	// epsilonPercentage float64
	incomingRequests chan hostEntryRequest
}

func (he *hostEntry) Host() string {
	// This never changes, so we can safely return it
	return he.host
}

func newHostEntry(host string, initialRetryDelay time.Duration, maxRetryInterval time.Duration) HostEntry {
	he := &hostEntry{
		host:              host,
		retryDelay:        initialRetryDelay,
		initialRetryDelay: initialRetryDelay,
		maxRetryInterval:  maxRetryInterval,
		incomingRequests:  make(chan hostEntryRequest),
	}
	go he.handleRequests()
	return he
}

func (he *hostEntry) handleRequests() {
	for req := range he.incomingRequests {
		var resp interface{}
		switch req.(type) {
		case *isDeadRequest:
			resp = he.dead
		case *setDeadRequest:
			newVal := req.(*setDeadRequest).setDeadTo
			if newVal && !he.dead {
				// Entering the deadpool - initialize retry
				he.retryDelay = he.initialRetryDelay
				he.nextRetry = time.Now().Add(he.retryDelay)
			}
			he.dead = newVal
		case *canTryRequest:
			resp = !he.dead || he.nextRetry.Before(req.(*canTryRequest).atTime)
		case *willRetryRequest:
			he.retryDelay = time.Duration(int64(math.Min(float64(he.retryDelay*2), float64(he.maxRetryInterval))))
			he.nextRetry = time.Now().Add(he.retryDelay)
		}
		req.getRespChan() <- resp
	}
}

func (he *hostEntry) IsDead() bool {
	req := &isDeadRequest{
		baseHostEntryRequest{
			respChan: make(chan interface{}),
		},
	}
	he.incomingRequests <- req
	resp := <-req.respChan
	isDeadResp, ok := resp.(bool)
	if !ok {
		// TODO
	}
	return isDeadResp
}

func (he *hostEntry) SetDead(newDeadVal bool) {
	req := &setDeadRequest{
		baseHostEntryRequest{
			respChan: make(chan interface{}),
		},
		newDeadVal,
	}
	he.incomingRequests <- req
	<-req.respChan
}

func (he *hostEntry) canTryHost(now time.Time) bool {
	req := &canTryRequest{
		baseHostEntryRequest{
			respChan: make(chan interface{}),
		},
		now,
	}
	he.incomingRequests <- req
	resp := <-req.respChan
	canTryResp, ok := resp.(bool)
	if !ok {
		// TODO
	}
	return canTryResp
}

func (he *hostEntry) willRetryHost() {
	req := &willRetryRequest{
		baseHostEntryRequest{
			respChan: make(chan interface{}),
		},
	}
	he.incomingRequests <- req
	<-req.respChan
}
