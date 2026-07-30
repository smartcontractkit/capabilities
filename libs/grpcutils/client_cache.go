package grpcutils

import (
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type ConnCache struct {
	mu    sync.RWMutex
	conns map[string]*grpc.ClientConn
}

func NewConnCache() *ConnCache {
	return &ConnCache{conns: map[string]*grpc.ClientConn{}}
}

func (c *ConnCache) GetConn(endpoint string) (*grpc.ClientConn, error) {
	// First check with read lock
	c.mu.RLock()
	conn, exists := c.conns[endpoint]
	c.mu.RUnlock()

	if exists {
		// Check if connection is still valid
		if conn.GetState().String() != "SHUTDOWN" {
			return conn, nil
		}
		// Connection is dead, remove it and create new one
		c.mu.Lock()
		delete(c.conns, endpoint)
		c.mu.Unlock()
	}

	// Create new connection with write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	conn, exists = c.conns[endpoint]
	if exists {
		return conn, nil
	}

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	c.conns[endpoint] = conn
	return conn, nil
}
