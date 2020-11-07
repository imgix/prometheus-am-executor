package chanmap

import "sync"

// Channel represents a channel that can only be closed once
type Channel struct {
	ch   chan struct{}
	once sync.Once
}

// ChannelMap manages a mapping of Channels.
// It's meant to help trigger an action across a group of listeners,
// without needing to handle details of group membership itself.
type ChannelMap struct {
	channels map[string]Channel
	sync.RWMutex
}

// Close closes the channel only once, so it's safe to call concurrently.
func (c *Channel) Close() {
	c.once.Do(func() { close(c.ch) })
}

// Add returns the control channel for a given key, creating it if necessary
func (cm *ChannelMap) Add(key string) chan struct{} {
	cm.Lock()
	defer cm.Unlock()
	c, ok := cm.channels[key]
	if ok {
		return c.ch
	}

	cm.channels[key] = Channel{
		ch:   make(chan struct{}),
		once: sync.Once{},
	}
	return cm.channels[key].ch
}

// Get returns the control channel for a given key
func (cm *ChannelMap) Get(key string) (chan struct{}, bool) {
	cm.RLock()
	defer cm.RUnlock()
	c, ok := cm.channels[key]
	return c.ch, ok
}

// Close closes a matching control channel and discards it
func (cm *ChannelMap) Close(key string) {
	cm.Lock()
	defer cm.Unlock()
	c, ok := cm.channels[key]
	if !ok {
		return
	}
	c.Close()
	delete(cm.channels, key)
}

// CloseAll closes all control channels
func (cm *ChannelMap) CloseAll() {
	cm.Lock()
	defer cm.Unlock()

	keys := make([]string, len(cm.channels))
	for k, c := range cm.channels {
		c.Close()
		keys = append(keys, k)
	}

	for _, k := range keys {
		delete(cm.channels, k)
	}
}

// NewChannelMap returns a ChannelMap instance
func NewChannelMap() *ChannelMap {
	return &ChannelMap{
		channels: make(map[string]Channel),
	}
}
