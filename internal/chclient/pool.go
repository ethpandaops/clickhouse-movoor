package chclient

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const (
	defaultMaxOpenConns = 4
	defaultMaxIdleConns = 2
)

// Config describes the ClickHouse client pool. Each NodeConfig is one physical
// ClickHouse node, not a failover group.
type Config struct {
	DialTimeout time.Duration
	Nodes       []NodeConfig
}

// NodeConfig describes one physical ClickHouse node.
type NodeConfig struct {
	Name    string
	Shard   string
	Replica string
	DSN     string
}

// Node identifies one configured ClickHouse node.
type Node struct {
	ID      string
	Shard   string
	Replica string
	Addr    string
}

// Client is a database/sql ClickHouse client bound to one configured node.
type Client struct {
	Node Node
	DB   *sql.DB
}

// Pool owns one ClickHouse client per configured physical node.
type Pool struct {
	clients []Client
}

// NewPool constructs one native clickhouse-go database handle per configured
// node. Handles are lazy: ClickHouse connections are opened by Ping or Query.
func NewPool(cfg Config) (*Pool, error) {
	if len(cfg.Nodes) == 0 {
		return nil, errors.New("at least one clickhouse node is required")
	}

	clients := make([]Client, 0, len(cfg.Nodes))
	for i, node := range cfg.Nodes {
		db, addr, err := openNode(node, cfg.DialTimeout)
		if err != nil {
			err = errors.Join(err, closeClients(clients))

			return nil, fmt.Errorf("node %d %q: %w", i, node.Name, err)
		}

		clients = append(clients, Client{
			Node: Node{
				ID:      node.Name,
				Shard:   node.Shard,
				Replica: node.Replica,
				Addr:    addr,
			},
			DB: db,
		})
	}

	return &Pool{clients: clients}, nil
}

// Clients returns a copy of the configured node clients.
func (p *Pool) Clients() []Client {
	if p == nil {
		return nil
	}

	clients := make([]Client, len(p.clients))
	copy(clients, p.clients)

	return clients
}

// Close closes every underlying database handle.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}

	return closeClients(p.clients)
}

func openNode(node NodeConfig, dialTimeout time.Duration) (*sql.DB, string, error) {
	if node.Name == "" {
		return nil, "", errors.New("name is required")
	}
	if node.Shard == "" {
		return nil, "", errors.New("shard is required")
	}
	if node.Replica == "" {
		return nil, "", errors.New("replica is required")
	}

	opts, err := clickhouse.ParseDSN(node.DSN)
	if err != nil {
		return nil, "", fmt.Errorf("parse dsn: %w", err)
	}
	if len(opts.Addr) != 1 {
		return nil, "", fmt.Errorf("dsn must contain exactly one address, got %d", len(opts.Addr))
	}
	if dialTimeout > 0 {
		opts.DialTimeout = dialTimeout
	}
	if opts.MaxOpenConns == 0 {
		opts.MaxOpenConns = defaultMaxOpenConns
	}
	if opts.MaxIdleConns == 0 {
		opts.MaxIdleConns = defaultMaxIdleConns
	}

	return clickhouse.OpenDB(opts), opts.Addr[0], nil
}

func closeClients(clients []Client) error {
	var err error
	for _, client := range clients {
		err = errors.Join(err, client.DB.Close())
	}

	return err
}
