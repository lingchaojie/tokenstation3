//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	release *GitHubRelease
	err     error
	repo    string
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(_ context.Context, repo string) (*GitHubRelease, error) {
	s.repo = repo
	if s.err != nil {
		return nil, s.err
	}
	return s.release, nil
}

func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(context.Context, string) ([]byte, error) {
	panic("FetchChecksumFile should not be called when no update is available")
}

func TestUpdateServiceCheckUpdateUsesDefaultGitHubRepo(t *testing.T) {
	githubClient := &updateServiceGitHubClientStub{
		release: &GitHubRelease{
			TagName: "v0.1.133",
			Name:    "v0.1.133",
		},
	}
	svc := NewUpdateService(&updateServiceCacheStub{}, githubClient, "0.1.132", "release")

	_, err := svc.CheckUpdate(context.Background(), true)

	require.NoError(t, err)
	require.Equal(t, "Wei-Shaw/sub2api", githubClient.repo)
}

func TestUpdateServiceCheckUpdateUsesConfiguredGitHubRepo(t *testing.T) {
	githubClient := &updateServiceGitHubClientStub{
		release: &GitHubRelease{
			TagName: "v0.1.133",
			Name:    "v0.1.133",
		},
	}
	svc := NewUpdateServiceWithGitHubRepo(
		&updateServiceCacheStub{},
		githubClient,
		"0.1.132",
		"release",
		"lingchaojie/tokenstation3",
	)

	_, err := svc.CheckUpdate(context.Background(), true)

	require.NoError(t, err)
	require.Equal(t, "lingchaojie/tokenstation3", githubClient.repo)
}

func TestUpdateServiceCheckUpdateIgnoresCacheFromDifferentGitHubRepo(t *testing.T) {
	cache := &updateServiceCacheStub{}
	defaultSvc := NewUpdateServiceWithGitHubRepo(
		cache,
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.133",
				Name:    "Default repo release",
			},
		},
		"0.1.132",
		"release",
		"Wei-Shaw/sub2api",
	)
	_, err := defaultSvc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)

	forkClient := &updateServiceGitHubClientStub{
		release: &GitHubRelease{
			TagName: "v0.1.134",
			Name:    "Fork repo release",
		},
	}
	forkSvc := NewUpdateServiceWithGitHubRepo(
		cache,
		forkClient,
		"0.1.132",
		"release",
		"lingchaojie/tokenstation3",
	)

	info, err := forkSvc.CheckUpdate(context.Background(), false)

	require.NoError(t, err)
	require.Equal(t, "lingchaojie/tokenstation3", forkClient.repo)
	require.Equal(t, "0.1.134", info.LatestVersion)
	require.False(t, info.Cached)
}

func TestUpdateServiceCheckUpdateUsesLegacyCacheForDefaultGitHubRepo(t *testing.T) {
	cache := &updateServiceCacheStub{
		data: `{"latest":"0.1.133","release_info":{"name":"Legacy default repo release"},"timestamp":9999999999}`,
	}
	svc := NewUpdateServiceWithGitHubRepo(
		cache,
		&updateServiceGitHubClientStub{err: errors.New("github unavailable")},
		"0.1.132",
		"release",
		"Wei-Shaw/sub2api",
	)

	info, err := svc.CheckUpdate(context.Background(), false)

	require.NoError(t, err)
	require.True(t, info.Cached)
	require.Equal(t, "0.1.133", info.LatestVersion)
}

func TestUpdateServiceCheckUpdateRejectsLegacyCacheForConfiguredGitHubRepo(t *testing.T) {
	cache := &updateServiceCacheStub{
		data: `{"latest":"0.1.133","release_info":{"name":"Legacy default repo release"},"timestamp":9999999999}`,
	}
	githubClient := &updateServiceGitHubClientStub{
		release: &GitHubRelease{
			TagName: "v0.1.134",
			Name:    "Fork repo release",
		},
	}
	svc := NewUpdateServiceWithGitHubRepo(
		cache,
		githubClient,
		"0.1.132",
		"release",
		"lingchaojie/tokenstation3",
	)

	info, err := svc.CheckUpdate(context.Background(), false)

	require.NoError(t, err)
	require.False(t, info.Cached)
	require.Equal(t, "lingchaojie/tokenstation3", githubClient.repo)
	require.Equal(t, "0.1.134", info.LatestVersion)
}

func TestUpdateServiceCheckUpdateDoesNotFallbackToCacheFromDifferentGitHubRepo(t *testing.T) {
	cache := &updateServiceCacheStub{}
	defaultSvc := NewUpdateServiceWithGitHubRepo(
		cache,
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.133",
				Name:    "Default repo release",
			},
		},
		"0.1.132",
		"release",
		"Wei-Shaw/sub2api",
	)
	_, err := defaultSvc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)

	forkSvc := NewUpdateServiceWithGitHubRepo(
		cache,
		&updateServiceGitHubClientStub{err: errors.New("fork unavailable")},
		"0.1.132",
		"release",
		"lingchaojie/tokenstation3",
	)

	info, err := forkSvc.CheckUpdate(context.Background(), true)

	require.NoError(t, err)
	require.False(t, info.HasUpdate)
	require.Equal(t, "0.1.132", info.LatestVersion)
	require.Contains(t, info.Warning, "fork unavailable")
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.132",
				Name:    "v0.1.132",
			},
		},
		"0.1.132",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoUpdateAvailable))
	require.ErrorIs(t, err, ErrNoUpdateAvailable)
}
