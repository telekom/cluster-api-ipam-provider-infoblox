package infoblox_test

import (
	"errors"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibmock"
	"go.uber.org/mock/gomock"
)

const rotatedPassword = "rotated-password"

func TestClientCacheReusesClient(t *testing.T) {
	g := NewWithT(t)
	cache := newTestClientCache(t)
	config := cacheTestConfig()

	first, err := cache.Get("instance-a", "1", "secret-a", "1", config)
	g.Expect(err).NotTo(HaveOccurred())
	second, err := cache.Get("instance-a", "1", "secret-a", "1", config)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(second).To(BeIdenticalTo(first))
}

func TestClientCacheForwardsConfig(t *testing.T) {
	g := NewWithT(t)
	expectedConfig := cacheTestConfig()
	var receivedConfig infoblox.Config
	cache := infoblox.NewClientCache(func(config infoblox.Config) (infoblox.Client, error) {
		receivedConfig = config
		return ibmock.NewMockClient(gomock.NewController(t)), nil
	})

	_, err := cache.Get("instance-a", "1", "secret-a", "1", expectedConfig)

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(receivedConfig).To(Equal(expectedConfig))
}

func TestClientCacheCreatesDifferentInstancesConcurrently(t *testing.T) {
	g := NewWithT(t)
	cache := newTestClientCache(t)
	config := cacheTestConfig()
	results := make(chan clientResult, 2)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup

	for _, instanceName := range []string{"instance-a", "instance-b"} {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			client, err := cache.Get(instanceName, "1", "secret-a", "1", config)
			results <- clientResult{client: client, err: err}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	clients := make([]infoblox.Client, 0, 2)
	for result := range results {
		g.Expect(result.err).NotTo(HaveOccurred())
		clients = append(clients, result.client)
	}
	g.Expect(clients).To(HaveLen(2))
	g.Expect(clients[0]).NotTo(BeIdenticalTo(clients[1]))
}

func TestClientCacheReplacesNewerChangedConfiguration(t *testing.T) {
	tests := []struct {
		name                    string
		instanceResourceVersion string
		secretResourceVersion   string
		changeConfig            func(infoblox.Config) infoblox.Config
	}{
		{
			name:                    "instance changed",
			instanceResourceVersion: "2",
			secretResourceVersion:   "1",
			changeConfig: func(config infoblox.Config) infoblox.Config {
				config.Host = "new-infoblox.example.test"
				return config
			},
		},
		{
			name:                    "Secret changed",
			instanceResourceVersion: "1",
			secretResourceVersion:   "2",
			changeConfig: func(config infoblox.Config) infoblox.Config {
				config.Password = rotatedPassword
				return config
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			cache := newTestClientCache(t)
			config := cacheTestConfig()

			first, err := cache.Get("instance-a", "1", "secret-a", "1", config)
			g.Expect(err).NotTo(HaveOccurred())
			second, err := cache.Get("instance-a", test.instanceResourceVersion, "secret-a", test.secretResourceVersion, test.changeConfig(config))
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(second).NotTo(BeIdenticalTo(first))
		})
	}
}

func TestClientCacheReplacesClientWhenCredentialsSecretChanges(t *testing.T) {
	g := NewWithT(t)
	cache := newTestClientCache(t)
	oldConfig := cacheTestConfig()
	newConfig := cacheTestConfig()
	newConfig.Password = "new-secret-password"

	oldClient, err := cache.Get("instance-a", "10", "secret-a", "100", oldConfig)
	g.Expect(err).NotTo(HaveOccurred())
	newClient, err := cache.Get("instance-a", "11", "secret-b", "1", newConfig)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(newClient).NotTo(BeIdenticalTo(oldClient))
	confirmedClient, err := cache.Get("instance-a", "11", "secret-b", "1", newConfig)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(confirmedClient).To(BeIdenticalTo(newClient))
}

func TestClientCacheReusesClientWhenResourceVersionChangesWithoutConfigChange(t *testing.T) {
	g := NewWithT(t)
	cache := newTestClientCache(t)
	config := cacheTestConfig()

	first, err := cache.Get("instance-a", "1", "secret-a", "1", config)
	g.Expect(err).NotTo(HaveOccurred())
	second, err := cache.Get("instance-a", "2", "secret-a", "2", config)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(second).To(BeIdenticalTo(first))
	third, err := cache.Get("instance-a", "2", "secret-a", "2", config)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(third).To(BeIdenticalTo(first))
}

func TestClientCacheDoesNotRollBackOnStaleRequest(t *testing.T) {
	g := NewWithT(t)
	cache := newTestClientCache(t)
	oldConfig := cacheTestConfig()
	newConfig := cacheTestConfig()
	newConfig.Password = rotatedPassword

	_, err := cache.Get("instance-a", "1", "secret-a", "1", oldConfig)
	g.Expect(err).NotTo(HaveOccurred())
	newClient, err := cache.Get("instance-a", "2", "secret-a", "2", newConfig)
	g.Expect(err).NotTo(HaveOccurred())
	staleClient, err := cache.Get("instance-a", "1", "secret-a", "1", oldConfig)
	g.Expect(err).NotTo(HaveOccurred())
	currentClient, err := cache.Get("instance-a", "2", "secret-a", "2", newConfig)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(staleClient).To(BeIdenticalTo(newClient))
	g.Expect(currentClient).To(BeIdenticalTo(newClient))
}

func TestClientCacheRejectsMixedConfigurationSnapshot(t *testing.T) {
	tests := []struct {
		name                    string
		cachedInstanceVersion   string
		cachedSecretVersion     string
		incomingInstanceVersion string
		incomingSecretVersion   string
		changeConfig            func(infoblox.Config) infoblox.Config
	}{
		{
			name:                    "older instance and newer Secret",
			cachedInstanceVersion:   "2",
			cachedSecretVersion:     "1",
			incomingInstanceVersion: "1",
			incomingSecretVersion:   "2",
			changeConfig: func(config infoblox.Config) infoblox.Config {
				config.Password = rotatedPassword
				return config
			},
		},
		{
			name:                    "newer instance and older Secret",
			cachedInstanceVersion:   "1",
			cachedSecretVersion:     "2",
			incomingInstanceVersion: "2",
			incomingSecretVersion:   "1",
			changeConfig: func(config infoblox.Config) infoblox.Config {
				config.Host = "new-infoblox.example.test"
				return config
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			cache := newTestClientCache(t)
			config := cacheTestConfig()
			cachedClient, err := cache.Get("instance-a", test.cachedInstanceVersion, "secret-a", test.cachedSecretVersion, config)
			g.Expect(err).NotTo(HaveOccurred())

			client, err := cache.Get("instance-a", test.incomingInstanceVersion, "secret-a", test.incomingSecretVersion, test.changeConfig(config))

			g.Expect(client).To(BeNil())
			g.Expect(errors.Is(err, infoblox.ErrMixedConfigurationSnapshot)).To(BeTrue())
			retainedClient, err := cache.Get("instance-a", test.cachedInstanceVersion, "secret-a", test.cachedSecretVersion, config)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(retainedClient).To(BeIdenticalTo(cachedClient))
		})
	}
}

func TestClientCacheDeduplicatesConcurrentCreation(t *testing.T) {
	g := NewWithT(t)
	cache := newTestClientCache(t)
	config := cacheTestConfig()
	const callers = 100
	results := make(chan clientResult, callers)
	var waitGroup sync.WaitGroup

	for range callers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			client, err := cache.Get("instance-a", "1", "secret-a", "1", config)
			results <- clientResult{client: client, err: err}
		}()
	}
	waitGroup.Wait()
	close(results)

	var first infoblox.Client
	for result := range results {
		g.Expect(result.err).NotTo(HaveOccurred())
		if first == nil {
			first = result.client
		}
		g.Expect(result.client).To(BeIdenticalTo(first))
	}
}

func TestClientCacheHandlesConcurrentVersions(t *testing.T) {
	g := NewWithT(t)
	cache := newTestClientCache(t)
	oldConfig := cacheTestConfig()
	newConfig := cacheTestConfig()
	newConfig.Password = rotatedPassword
	const callers = 100
	results := make(chan clientResult, callers)
	var waitGroup sync.WaitGroup

	for index := range callers {
		instanceVersion, secretVersion, config := "1", "1", oldConfig
		if index%2 == 0 {
			instanceVersion, secretVersion, config = "2", "2", newConfig
		}
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			client, err := cache.Get("instance-a", instanceVersion, "secret-a", secretVersion, config)
			results <- clientResult{client: client, err: err}
		}()
	}
	waitGroup.Wait()
	close(results)

	for result := range results {
		g.Expect(result.err).NotTo(HaveOccurred())
		g.Expect(result.client).NotTo(BeNil())
	}
	finalClient, err := cache.Get("instance-a", "2", "secret-a", "2", newConfig)
	g.Expect(err).NotTo(HaveOccurred())
	confirmedClient, err := cache.Get("instance-a", "2", "secret-a", "2", newConfig)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(confirmedClient).To(BeIdenticalTo(finalClient))
}

func TestClientCacheDoesNotCacheErrors(t *testing.T) {
	g := NewWithT(t)
	attempts := 0
	expectedErr := errors.New("client creation failed")
	ctrl := gomock.NewController(t)
	cache := infoblox.NewClientCache(func(infoblox.Config) (infoblox.Client, error) {
		attempts++
		if attempts == 1 {
			return nil, expectedErr
		}
		return ibmock.NewMockClient(ctrl), nil
	})
	config := cacheTestConfig()

	_, err := cache.Get("instance-a", "1", "secret-a", "1", config)
	g.Expect(err).To(MatchError(expectedErr))
	client, err := cache.Get("instance-a", "1", "secret-a", "1", config)

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(client).NotTo(BeNil())
	g.Expect(attempts).To(Equal(2))
}

func TestClientCacheDeleteEvictsInstance(t *testing.T) {
	g := NewWithT(t)
	cache := newTestClientCache(t)
	config := cacheTestConfig()

	first, err := cache.Get("instance-a", "1", "secret-a", "1", config)
	g.Expect(err).NotTo(HaveOccurred())
	otherClient, err := cache.Get("instance-b", "1", "secret-a", "1", config)
	g.Expect(err).NotTo(HaveOccurred())

	cache.Delete("instance-a")

	recreatedClient, err := cache.Get("instance-a", "1", "secret-a", "1", config)
	g.Expect(err).NotTo(HaveOccurred())
	retainedOtherClient, err := cache.Get("instance-b", "1", "secret-a", "1", config)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(recreatedClient).NotTo(BeIdenticalTo(first))
	g.Expect(retainedOtherClient).To(BeIdenticalTo(otherClient))
}

func TestClientCacheDeleteRacesWithGet(t *testing.T) {
	g := NewWithT(t)
	cache := newTestClientCache(t)
	config := cacheTestConfig()
	first, err := cache.Get("instance-a", "1", "secret-a", "1", config)
	g.Expect(err).NotTo(HaveOccurred())
	start := make(chan struct{})
	result := make(chan clientResult, 1)
	var waitGroup sync.WaitGroup

	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		<-start
		client, err := cache.Get("instance-a", "1", "secret-a", "1", config)
		result <- clientResult{client: client, err: err}
	}()
	go func() {
		defer waitGroup.Done()
		<-start
		cache.Delete("instance-a")
	}()
	close(start)
	waitGroup.Wait()
	close(result)
	g.Expect((<-result).err).NotTo(HaveOccurred())

	finalClient, err := cache.Get("instance-a", "1", "secret-a", "1", config)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(finalClient).NotTo(BeIdenticalTo(first))
}

func newTestClientCache(t *testing.T) *infoblox.ClientCache {
	t.Helper()
	ctrl := gomock.NewController(t)
	return infoblox.NewClientCache(func(infoblox.Config) (infoblox.Client, error) {
		return ibmock.NewMockClient(ctrl), nil
	})
}

type clientResult struct {
	client infoblox.Client
	err    error
}

func cacheTestConfig() infoblox.Config {
	return infoblox.Config{
		HostConfig: infoblox.HostConfig{
			Host:                   "infoblox.example.test",
			Port:                   "443",
			Version:                "2.12",
			DisableTLSVerification: true,
			DefaultNetworkView:     "default",
			DefaultDNSView:         "default",
		},
		AuthConfig: infoblox.AuthConfig{
			Username: "benchmark",
			Password: "password",
		},
	}
}
