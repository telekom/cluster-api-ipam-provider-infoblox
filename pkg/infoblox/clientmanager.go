package infoblox

import "fmt"

type clientManager struct {
	clients map[string]Client
}

// ClientManager takes care of creating and caching clients.
type ClientManager interface {
	GetOrCreateFromInstanceParams(namespace, name string, config Config) (Client, error)
}

func clientKey(namespace, name string) string {
	return namespace + "/" + name
}

func (c *clientManager) GetOrCreateFromInstanceParams(namespace, name string, config Config) (Client, error) {
	cl, ok := c.clients[clientKey(namespace, name)]
	if ok {
		return cl, nil
	}
	client, err := NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	c.clients[clientKey(namespace, name)] = client
	return client, nil
}

func (c *clientManager) Get(namespace, name string) Client {
	return c.clients[clientKey(namespace, name)]
}

func (c *clientManager) Set(namespace, name string, cl Client) {
	c.clients[clientKey(namespace, name)] = cl
}
