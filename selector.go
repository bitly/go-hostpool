package hostpool

import (
	"log"
	"sync"
	"time"
)

type Selector interface {
	Init([]string)
	SelectNextHost() string
	MakeHostResponse(string) HostPoolResponse
	MarkHost(string, error)
	ResetAll()
}

type standardSelector struct {
	sync.RWMutex
	hosts             map[string]*hostEntry
	hostList          []*hostEntry
	initialRetryDelay time.Duration
	maxRetryInterval  time.Duration
	nextHostIndex     int
}

func (s *standardSelector) Init(hosts []string) {
	s.hosts = make(map[string]*hostEntry, len(hosts))
	s.hostList = make([]*hostEntry, len(hosts))
	s.initialRetryDelay = time.Duration(30) * time.Second
	s.maxRetryInterval = time.Duration(900) * time.Second

	for i, h := range hosts {
		e := &hostEntry{
			host:       h,
			retryDelay: s.initialRetryDelay,
		}
		s.hosts[h] = e
		s.hostList[i] = e
	}
}

func (s *standardSelector) SelectNextHost() string {
	s.Lock()
	host := s.getRoundRobin()
	s.Unlock()
	return host
}

func (s *standardSelector) getRoundRobin() string {
	now := time.Now()
	hostCount := len(s.hostList)
	for i := range s.hostList {
		// iterate via sequenece from where we last iterated
		currentIndex := (i + s.nextHostIndex) % hostCount

		h := s.hostList[currentIndex]
		if h.canTryHost(now) {
			s.nextHostIndex = currentIndex + 1
			return h.host
		}
	}

	// all hosts are down. re-add them
	s.doResetAll()
	s.nextHostIndex = 0
	return s.hostList[0].host
}

func (s *standardSelector) MakeHostResponse(host string) HostPoolResponse {
	s.Lock()
	defer s.Unlock()
	h, ok := s.hosts[host]
	if !ok {
		log.Fatalf("host %s not in HostPool", host)
	}
	now := time.Now()
	if h.dead && h.nextRetry.Before(now) {
		h.willRetryHost(s.maxRetryInterval)
	}
	return &standardHostPoolResponse{host: host, ss: s}
}

func (s *standardSelector) MarkHost(host string, err error) {
	s.Lock()
	defer s.Unlock()

	h, ok := s.hosts[host]
	if !ok {
		log.Fatalf("host %s not in HostPool", host)
	}
	if err == nil {
		// success - mark host alive
		h.dead = false
	} else {
		// failure - mark host dead
		if !h.dead {
			h.dead = true
			h.retryCount = 0
			h.retryDelay = s.initialRetryDelay
			h.nextRetry = time.Now().Add(h.retryDelay)
		}
	}
}

func (s *standardSelector) ResetAll() {
	s.Lock()
	defer s.Unlock()
	s.doResetAll()
}

// this actually performs the logic to reset,
// and should only be called when the lock has
// already been acquired
func (s *standardSelector) doResetAll() {
	for _, h := range s.hosts {
		h.dead = false
	}
}
