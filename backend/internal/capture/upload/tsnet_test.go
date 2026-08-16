package upload

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTSNetDialerUsesPersistentTaggedNodeWithoutStartingIt(t *testing.T) {
	factory := &recordingTSNetFactory{node: &fakeTSNetNode{}}
	dialer, err := NewTSNetDialer(TSNetConfig{
		Dir:      t.TempDir(),
		Hostname: "sub2api-capture-writer",
		AuthKey:  "tskey-auth-test",
		Factory:  factory.New,
	})

	require.NoError(t, err)
	require.False(t, factory.config.Ephemeral)
	require.Equal(t, "sub2api-capture-writer", factory.config.Hostname)
	require.Equal(t, "tskey-auth-test", factory.config.AuthKey)
	require.NotEmpty(t, factory.config.Dir)
	node, ok := factory.node.(*fakeTSNetNode)
	require.True(t, ok)
	require.Zero(t, node.dialCalls)
	require.NoError(t, dialer.Close())
	require.NoError(t, dialer.Close())
	require.Zero(t, node.closeCalls)
}

func TestTSNetDialerCloseWaitsForLazyStartAndRejectsLaterDials(t *testing.T) {
	node := &fakeTSNetNode{dialStarted: make(chan struct{}), releaseDial: make(chan struct{})}
	dialer, err := NewTSNetDialer(TSNetConfig{
		Dir: t.TempDir(), Hostname: "capture-writer", AuthKey: "tskey-auth-test",
		Factory: (&recordingTSNetFactory{node: node}).New,
	})
	require.NoError(t, err)
	dialDone := make(chan error, 1)
	go func() {
		_, dialErr := dialer.DialContext(context.Background(), "tcp", "clickhouse.internal:8123")
		dialDone <- dialErr
	}()
	<-node.dialStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- dialer.Close() }()
	require.Never(t, func() bool {
		node.mu.Lock()
		defer node.mu.Unlock()
		return node.closeCalls != 0
	}, 20*time.Millisecond, time.Millisecond)

	close(node.releaseDial)
	require.NoError(t, <-dialDone)
	require.NoError(t, <-closeDone)
	require.Equal(t, 1, node.closeCalls)
	_, err = dialer.DialContext(context.Background(), "tcp", "clickhouse.internal:8123")
	require.EqualError(t, err, "embedded tailnet dialer is closed")
}

func TestTSNetDialerDelegatesDialAndSanitizesNodeErrors(t *testing.T) {
	const authKey = "tskey-auth-super-secret"
	var logs bytes.Buffer
	node := &fakeTSNetNode{dialErr: errors.New("provider rejected " + authKey)}
	factory := &recordingTSNetFactory{node: node}
	dialer, err := NewTSNetDialer(TSNetConfig{
		Dir:      t.TempDir(),
		Hostname: "capture-writer",
		AuthKey:  authKey,
		Logger:   slog.New(slog.NewJSONHandler(&logs, nil)),
		Factory:  factory.New,
	})
	require.NoError(t, err)
	factory.config.Logf("unsafe upstream event %s", authKey)

	_, err = dialer.DialContext(context.Background(), "tcp", "clickhouse.internal:8123")

	require.EqualError(t, err, "embedded tailnet dial failed")
	require.NotContains(t, err.Error(), authKey)
	require.NotContains(t, logs.String(), authKey)
	require.NotContains(t, logs.String(), "clickhouse.internal")
}

func TestTSNetDialerSanitizesConstructionAndCloseErrors(t *testing.T) {
	const authKey = "tskey-auth-never-print"
	_, err := NewTSNetDialer(TSNetConfig{
		Dir:      t.TempDir(),
		Hostname: "capture-writer",
		AuthKey:  authKey,
		Factory: func(TSNetServerConfig) (TSNetServer, error) {
			return nil, errors.New("factory exposed " + authKey)
		},
	})
	require.EqualError(t, err, "embedded tailnet construction failed")
	require.NotContains(t, err.Error(), authKey)

	node := &fakeTSNetNode{closeErr: errors.New("close exposed " + authKey)}
	dialer, err := NewTSNetDialer(TSNetConfig{
		Dir: t.TempDir(), Hostname: "capture-writer", AuthKey: authKey,
		Factory: (&recordingTSNetFactory{node: node}).New,
	})
	require.NoError(t, err)
	_, err = dialer.DialContext(context.Background(), "tcp", "clickhouse.internal:8123")
	require.NoError(t, err)
	err = dialer.Close()
	require.EqualError(t, err, "embedded tailnet shutdown failed")
	require.NotContains(t, err.Error(), authKey)
}

func TestTSNetDialerRejectsUnsafeConfigurationWithoutCallingFactory(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  TSNetConfig
	}{
		{name: "missing state directory", cfg: TSNetConfig{Hostname: "writer", AuthKey: "secret"}},
		{name: "missing hostname", cfg: TSNetConfig{Dir: t.TempDir(), AuthKey: "secret"}},
		{name: "missing tagged auth key", cfg: TSNetConfig{Dir: t.TempDir(), Hostname: "writer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			tc.cfg.Factory = func(TSNetServerConfig) (TSNetServer, error) {
				called = true
				return &fakeTSNetNode{}, nil
			}
			_, err := NewTSNetDialer(tc.cfg)
			require.Error(t, err)
			require.False(t, called)
			require.False(t, strings.Contains(err.Error(), "secret"))
		})
	}
}

type recordingTSNetFactory struct {
	config TSNetServerConfig
	node   TSNetServer
}

func (f *recordingTSNetFactory) New(config TSNetServerConfig) (TSNetServer, error) {
	f.config = config
	return f.node, nil
}

type fakeTSNetNode struct {
	mu          sync.Mutex
	dialCalls   int
	closeCalls  int
	dialErr     error
	closeErr    error
	dialStarted chan struct{}
	releaseDial chan struct{}
}

func (n *fakeTSNetNode) Dial(context.Context, string, string) (net.Conn, error) {
	n.mu.Lock()
	n.dialCalls++
	started := n.dialStarted
	release := n.releaseDial
	err := n.dialErr
	n.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		<-release
	}
	return nil, err
}

func (n *fakeTSNetNode) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closeCalls++
	return n.closeErr
}
