package chanmap

import "testing"

const (
	// Using the same test key for tests run in parallel helps to confirm that
	// new Counter instances are unique.
	testKey = "banana"
)

func TestChannel_Close(t *testing.T) {
	t.Parallel()

	var cm = NewChannelMap()
	_ = cm.Add(testKey)

	c, ok := cm.channels[testKey]
	if !ok {
		t.Errorf("missing channel for key '%s'", testKey)
	}

	go func() { c.ch <- struct{}{} }()
	_, ok = <-c.ch
	if !ok {
		t.Errorf("channel is closed when it should be open for key '%s'", testKey)
	}

	c.Close()
	_, ok = <-c.ch
	if ok {
		t.Errorf("channel is open when it should be closed for key '%s'", testKey)
	}

	c.Close()
	_, ok = <-c.ch
	if ok {
		t.Errorf("channel is open when it should be closed for key '%s'", testKey)
	}
}

func TestChannelMap_Add(t *testing.T) {
	t.Parallel()

	var cm = NewChannelMap()
	_ = cm.Add(testKey)

	c, ok := cm.channels[testKey]
	if !ok {
		t.Errorf("missing channel for key '%s'", testKey)
	}
	if c.ch == nil {
		t.Errorf("channel is nil for key '%s'", testKey)
	}
}

func TestChannelMap_Close(t *testing.T) {
	t.Parallel()

	var cm = NewChannelMap()
	_ = cm.Add(testKey)

	c, ok := cm.channels[testKey]
	if !ok {
		t.Errorf("missing channel for key '%s'", testKey)
	}

	go func() { c.ch <- struct{}{} }()
	_, ok = <-c.ch
	if !ok {
		t.Errorf("channel is closed when it should be open for key '%s'", testKey)
	}

	cm.Close(testKey)
	_, ok = <-c.ch
	if ok {
		t.Errorf("channel is open when it should be closed for key '%s'", testKey)
	}

	cm.Close(testKey)
	_, ok = <-c.ch
	if ok {
		t.Errorf("channel still exists for key '%s'", testKey)
	}
}

func TestChannelMap_CloseAll(t *testing.T) {
	t.Parallel()

	var cm = NewChannelMap()
	_ = cm.Add(testKey)

	cm.CloseAll()
	if len(cm.channels) > 0 {
		t.Error("channels still exist")
	}
}

func TestChannelMap_Get(t *testing.T) {
	t.Parallel()

	var cm = NewChannelMap()
	_ = cm.Add(testKey)

	ch, ok := cm.Get(testKey)
	if !ok {
		t.Errorf("missing channel for key '%s'", testKey)
	}

	close(ch)

	c, ok := cm.channels[testKey]
	if !ok {
		t.Errorf("missing channel for key '%s'", testKey)
	}

	_, ok = <-c.ch
	if ok {
		t.Errorf("wrong channel was returned for key '%s'", testKey)
	}
}
