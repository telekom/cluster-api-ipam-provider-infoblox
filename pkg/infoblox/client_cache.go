package infoblox

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/resourceversion"
)

// ErrMixedConfigurationSnapshot indicates that the instance and Secret were read at inconsistent versions.
var ErrMixedConfigurationSnapshot = errors.New("mixed InfobloxInstance and Secret versions")

// GetClientFunc resolves a client for an InfobloxInstance and its configuration sources.
type GetClientFunc func(instanceName, instanceResourceVersion string, secretUID types.UID, secretResourceVersion string, config Config) (Client, error)

type configSource struct {
	instanceResourceVersion string
	secretUID               types.UID
	secretResourceVersion   string
}

type configSourceState int

const (
	configSourceCurrent configSourceState = iota
	configSourceStale
	configSourceMixed
)

func (s configSource) compare(cached configSource) (configSourceState, error) {
	instanceComparison, err := resourceversion.CompareResourceVersion(s.instanceResourceVersion, cached.instanceResourceVersion)
	if err != nil {
		return configSourceCurrent, fmt.Errorf("compare InfobloxInstance resource versions: %w", err)
	}
	instanceIsOlder := instanceComparison < 0
	instanceIsNewer := instanceComparison > 0

	// A different UID identifies a replacement Secret, regardless of its resource version.
	secretIsOlder := false
	secretIsNewer := s.secretUID != cached.secretUID
	if s.secretUID == cached.secretUID {
		secretComparison, err := resourceversion.CompareResourceVersion(s.secretResourceVersion, cached.secretResourceVersion)
		if err != nil {
			return configSourceCurrent, fmt.Errorf("compare Secret resource versions: %w", err)
		}
		secretIsOlder = secretComparison < 0
		secretIsNewer = secretComparison > 0
	}

	switch {
	case instanceIsOlder && secretIsNewer, instanceIsNewer && secretIsOlder:
		return configSourceMixed, nil
	case instanceIsOlder || secretIsOlder:
		return configSourceStale, nil
	default:
		return configSourceCurrent, nil
	}
}

type clientCacheEntry struct {
	source configSource
	config Config
	client Client
}

// ClientCache maintains one current client per InfobloxInstance.
type ClientCache struct {
	mu            sync.Mutex
	clients       map[string]clientCacheEntry
	newClientFunc func(Config) (Client, error)
}

// NewClientCache creates a client cache backed by the given client factory.
func NewClientCache(newClientFunc func(Config) (Client, error)) *ClientCache {
	return &ClientCache{
		clients:       make(map[string]clientCacheEntry),
		newClientFunc: newClientFunc,
	}
}

// Get returns the current client for an instance without allowing stale reconciliations to replace it.
func (c *ClientCache) Get(instanceName, instanceResourceVersion string, secretUID types.UID, secretResourceVersion string, config Config) (Client, error) {
	source := configSource{
		instanceResourceVersion: instanceResourceVersion,
		secretUID:               secretUID,
		secretResourceVersion:   secretResourceVersion,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if cachedEntry, ok := c.clients[instanceName]; ok {
		state, err := source.compare(cachedEntry.source)
		if err != nil {
			return nil, err
		}

		switch state {
		case configSourceMixed:
			return nil, fmt.Errorf("%w for instance %q", ErrMixedConfigurationSnapshot, instanceName)
		case configSourceStale:
			// Keep the current client when an older reconciliation finishes late.
			return cachedEntry.client, nil
		}
		// Reuse the client for metadata or status updates that do not affect its configuration.
		if reflect.DeepEqual(config, cachedEntry.config) {
			cachedEntry.source = source
			c.clients[instanceName] = cachedEntry
			return cachedEntry.client, nil
		}
	}

	newClient, err := c.newClientFunc(config)
	if err != nil {
		return nil, err
	}
	c.clients[instanceName] = clientCacheEntry{
		source: source,
		config: config,
		client: newClient,
	}
	return newClient, nil
}

// Delete removes the client owned by an InfobloxInstance.
func (c *ClientCache) Delete(instanceName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.clients, instanceName)
}
