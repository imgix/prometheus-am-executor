package countermap

import (
	"sync"
	"sync/atomic"
)

type msgKind int

const (
	getValue msgKind = iota
	setValue
	incValue
	decValue
	delValue
)

const (
	off int32 = iota
	on
)

// msgAnswer represents an answer to a msg
type msgAnswer struct {
	value int
	ok    bool
}

// msg represents a message that can be sent to the counter
type msg struct {
	kind   msgKind
	key    string
	value  int
	answer chan msgAnswer
}

// Counter tracks values for unique keys
type Counter struct {
	in      chan msg
	quit    chan struct{}
	wg      sync.WaitGroup
	started int32
}

// handler responds to messages sent to the Counter
// This function is meant to be called in a goroutine.
// Counter state is only available in this scope.
func (c *Counter) handler() {
	defer c.wg.Done()
	var counts = make(map[string]int)

	for {
		select {
		case m := <-c.in:
			switch m.kind {
			case getValue:
				v, ok := counts[m.key]
				m.answer <- msgAnswer{value: v, ok: ok}
			case setValue:
				counts[m.key] = m.value
			case incValue:
				_, ok := counts[m.key]
				if !ok {
					counts[m.key] = m.value
				} else {
					counts[m.key] += m.value
				}
			case decValue:
				_, ok := counts[m.key]
				if !ok {
					counts[m.key] = -m.value
				} else {
					counts[m.key] -= m.value
				}
			case delValue:
				delete(counts, m.key)
			}
			if m.answer != nil {
				close(m.answer)
			}
		case <-c.quit:
			return
		}
	}
}

// turnOff attempts to mark the counter as being off (can't serve requests)
// Returns true if it was turned off
func (c *Counter) turnOff() bool {
	return atomic.CompareAndSwapInt32(&c.started, on, off)
}

// turnOn attempts to mark the counter as being on (can serve requests)
// Returns true if it was turned on
func (c *Counter) turnOn() bool {
	return atomic.CompareAndSwapInt32(&c.started, off, on)
}

// Dec decrements the counter by 1
func (c *Counter) Dec(key string) {
	c.DecBy(key, 1)
}

// DecBy decrements the counter by the given amount
func (c *Counter) DecBy(key string, amt int) {
	c.in <- msg{kind: decValue, key: key, value: amt}
}

// Delete removes the counter
func (c *Counter) Delete(key string) {
	c.in <- msg{kind: delValue, key: key}
}

// Get returns the current value of the counter, and if it exists
func (c *Counter) Get(key string) (int, bool) {
	resp := make(chan msgAnswer)
	c.in <- msg{kind: getValue, key: key, answer: resp}
	a := <-resp
	return a.value, a.ok
}

// Inc increments the counter by 1
func (c *Counter) Inc(key string) {
	c.IncBy(key, 1)
}

// IncBy increments the counter by the given amount
func (c *Counter) IncBy(key string, amt int) {
	c.in <- msg{kind: incValue, key: key, value: amt}
}

// Reset sets the counter to zero
func (c *Counter) Reset(key string) {
	c.Set(key, 0)
}

// Set the counter to the given amount
func (c *Counter) Set(key string, to int) {
	c.in <- msg{kind: setValue, key: key, value: to}
}

// Start a counter message handler goroutine
func (c *Counter) Start() {
	if !c.turnOn() {
		// Counter is already running
		return
	}
	c.wg.Add(1)
	go c.handler()
}

// Stop a counter message handler goroutine
func (c *Counter) Stop() {
	if !c.turnOff() {
		// Counter is already stopped
		return
	}
	close(c.quit)
	c.wg.Wait()
}

func NewCounter() *Counter {
	c := Counter{
		in:   make(chan msg),
		quit: make(chan struct{}),
	}

	c.Start()
	return &c
}
