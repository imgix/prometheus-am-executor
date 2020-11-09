package countermap

import (
	"testing"
	"time"
)

const (
	// Using the same test key for tests run in parallel helps to confirm that
	// new Counter instances are unique.
	testKey = "banana"
)

func TestCounter_Dec(t *testing.T) {
	t.Parallel()
	var c = NewCounter()
	defer c.Stop()

	c.Dec(testKey)
	v, ok := c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != -1 {
		t.Errorf("wrong counter value; got %d, want %d", v, -1)
	}

	c.IncBy(testKey, 3)
	c.Dec(testKey)
	v, ok = c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != 1 {
		t.Errorf("wrong counter value; got %d, want %d", v, 1)
	}
}

func TestCounter_DecBy(t *testing.T) {
	t.Parallel()
	var c = NewCounter()
	defer c.Stop()

	c.DecBy(testKey, 4)
	v, ok := c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != -4 {
		t.Errorf("wrong counter value; got %d, want %d", v, -4)
	}
}

func TestCounter_Delete(t *testing.T) {
	t.Parallel()
	var c = NewCounter()
	defer c.Stop()

	c.Inc(testKey)
	v, ok := c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != 1 {
		t.Errorf("wrong counter value; got %d, want %d", v, 1)
	}

	c.Delete(testKey)

	v, ok = c.Get(testKey)
	if ok {
		t.Errorf("counter retrieved for non-existent key '%s'", testKey)
	}
	if v != 0 {
		t.Errorf("wrong nil value for counter of non-existent key; got %d, want %d", v, 0)
	}
}

func TestCounter_Get(t *testing.T) {
	t.Parallel()
	var c = NewCounter()
	defer c.Stop()

	v, ok := c.Get(testKey)
	if ok {
		t.Errorf("counter retrieved for non-existent key '%s'", testKey)
	}
	if v != 0 {
		t.Errorf("wrong nil value for counter of non-existent key; got %d, want %d", v, 0)
	}

	c.Inc(testKey)
	v, ok = c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != 1 {
		t.Errorf("wrong counter value; got %d, want %d", v, 1)
	}
}

func TestCounter_Inc(t *testing.T) {
	t.Parallel()
	var c = NewCounter()
	defer c.Stop()

	c.Inc(testKey)
	c.Inc(testKey)
	v, ok := c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != 2 {
		t.Errorf("wrong counter value; got %d, want %d", v, 2)
	}

	c.Dec(testKey)
	c.Inc(testKey)
	c.Inc(testKey)
	v, ok = c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != 3 {
		t.Errorf("wrong counter value; got %d, want %d", v, 3)
	}
}

func TestCounter_IncBy(t *testing.T) {
	t.Parallel()
	var c = NewCounter()
	defer c.Stop()

	c.IncBy(testKey, 4)
	v, ok := c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != 4 {
		t.Errorf("wrong counter value; got %d, want %d", v, 4)
	}

	c.IncBy(testKey, -9)
	v, ok = c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != -5 {
		t.Errorf("wrong counter value; got %d, want %d", v, -5)
	}
}

func TestCounter_Set(t *testing.T) {
	t.Parallel()
	var c = NewCounter()
	defer c.Stop()

	c.Set(testKey, 99)
	v, ok := c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != 99 {
		t.Errorf("wrong counter value; got %d, want %d", v, 99)
	}

	c.Set(testKey, 2018)
	v, ok = c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != 2018 {
		t.Errorf("wrong counter value; got %d, want %d", v, 2018)
	}
}

func TestCounter_Reset(t *testing.T) {
	t.Parallel()
	var c = NewCounter()
	defer c.Stop()

	c.Set(testKey, -23)
	v, ok := c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != -23 {
		t.Errorf("wrong counter value; got %d, want %d", v, -23)
	}

	c.Reset(testKey)

	v, ok = c.Get(testKey)
	if !ok {
		t.Errorf("missing counter for key '%s'", testKey)
	}
	if v != 0 {
		t.Errorf("wrong counter value; got %d, want %d", v, 0)
	}
}

func TestCounter_Start(t *testing.T) {
	t.Parallel()
	var c = NewCounter()
	defer c.Stop()
	var threshold = time.Duration(4) * time.Second

	if c.started != on {
		t.Errorf("counter not started; got %d, want %d", c.started, on)
	}

	// Counter should respond to requests
	done := make(chan struct{})
	expired := time.NewTimer(threshold)
	defer expired.Stop()
	go func() {
		c.Inc("banana")
		close(done)
	}()
	select {
	case <-done:
	case <-expired.C:
		t.Errorf("counter not responding to messages within %s", threshold)
	}
}

func TestCounter_Stop(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping due to -test.short flag")
	}
	t.Parallel()
	var c = NewCounter()
	var threshold = time.Duration(4) * time.Second

	c.Stop()

	if c.started != off {
		t.Errorf("counter not stopped; got %d, want %d", c.started, off)
	}

	// Counter shouldn't respond to requests
	done := make(chan struct{})
	expired := time.NewTimer(threshold)
	defer expired.Stop()
	go func() {
		c.Inc("banana")
		close(done)
	}()
	select {
	case <-done:
		t.Error("counter shouldn't respond to messages when stopped")
	case <-expired.C:
	}
}

func TestNewCounter(t *testing.T) {
	t.Parallel()
	var c = NewCounter()

	if c.in == nil {
		t.Error("counter missing 'in' channel")
	}

	if c.quit == nil {
		t.Error("counter missing 'quit' channel")
	}

	if c.started != on {
		t.Errorf("wrong started value; got %d, want %d", c.started, on)
	}
}
