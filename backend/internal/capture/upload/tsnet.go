package upload

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"

	"tailscale.com/tsnet"
)

// TSNetServer is the narrow embedded-tailnet surface used by the uploader.
type TSNetServer interface {
	Dial(context.Context, string, string) (net.Conn, error)
	Close() error
}

// TSNetServerConfig describes a persistent tsnet node without starting it.
// AuthKey is intentionally excluded from errors and logs by the dialer.
type TSNetServerConfig struct {
	Dir       string
	Hostname  string
	AuthKey   string
	Ephemeral bool
	Logf      func(string, ...any)
}

type TSNetFactory func(TSNetServerConfig) (TSNetServer, error)

type TSNetConfig struct {
	Dir      string
	Hostname string
	AuthKey  string
	Logger   *slog.Logger
	Factory  TSNetFactory
}

// TSNetDialer owns one persistent embedded Tailscale node. Constructing it does
// not connect to a tailnet; tsnet starts lazily on the first DialContext call.
type TSNetDialer struct {
	node TSNetServer

	mu       sync.Mutex
	started  bool
	closed   bool
	closeErr error
	closeOne sync.Once
}

func NewTSNetDialer(config TSNetConfig) (*TSNetDialer, error) {
	if strings.TrimSpace(config.Dir) == "" {
		return nil, errors.New("embedded tailnet state directory is required")
	}
	if strings.TrimSpace(config.Hostname) == "" {
		return nil, errors.New("embedded tailnet hostname is required")
	}
	if strings.TrimSpace(config.AuthKey) == "" {
		return nil, errors.New("embedded tailnet auth key is required")
	}
	factory := config.Factory
	if factory == nil {
		factory = newTSNetServer
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	node, err := factory(TSNetServerConfig{
		Dir:       config.Dir,
		Hostname:  config.Hostname,
		AuthKey:   config.AuthKey,
		Ephemeral: false,
		Logf: func(string, ...any) {
			logger.Debug("embedded tailnet event")
		},
	})
	if err != nil || node == nil {
		return nil, errors.New("embedded tailnet construction failed")
	}
	return &TSNetDialer{node: node}, nil
}

func newTSNetServer(config TSNetServerConfig) (TSNetServer, error) {
	return &tsnet.Server{
		Dir:       config.Dir,
		Hostname:  config.Hostname,
		AuthKey:   config.AuthKey,
		Ephemeral: config.Ephemeral,
		UserLogf:  config.Logf,
		Logf:      nil,
	}, nil
}

func (d *TSNetDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d == nil {
		return nil, errors.New("embedded tailnet dialer is unavailable")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("embedded tailnet dialer is closed")
	}
	d.started = true
	conn, err := d.node.Dial(ctx, network, address)
	if err != nil {
		return nil, errors.New("embedded tailnet dial failed")
	}
	return conn, nil
}

func (d *TSNetDialer) Close() error {
	if d == nil {
		return nil
	}
	d.closeOne.Do(func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.closed = true
		if d.started {
			if err := d.node.Close(); err != nil {
				d.closeErr = errors.New("embedded tailnet shutdown failed")
			}
		}
	})
	return d.closeErr
}
