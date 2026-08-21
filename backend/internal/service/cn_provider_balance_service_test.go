package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type cnBalanceResponseUpstream struct {
	status     int
	body       string
	bodyReader io.ReadCloser
}

func (u *cnBalanceResponseUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	body := u.bodyReader
	if body == nil {
		body = io.NopCloser(strings.NewReader(u.body))
	}
	return &http.Response{
		StatusCode: u.status,
		Header:     make(http.Header),
		Body:       body,
	}, nil
}

func (u *cnBalanceResponseUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

type errorAfterDataReadCloser struct {
	data []byte
	done bool
}

func (r *errorAfterDataReadCloser) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), io.ErrUnexpectedEOF
}

func (r *errorAfterDataReadCloser) Close() error { return nil }

type cnBalanceStateRepo struct {
	AccountRepository
	account    *Account
	updates    []map[string]any
	pauseCalls int
	clearCalls int
}

func (r *cnBalanceStateRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *cnBalanceStateRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updates = append(r.updates, updates)
	return nil
}

func (r *cnBalanceStateRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.pauseCalls++
	return nil
}

func (r *cnBalanceStateRepo) ClearTempUnschedulable(context.Context, int64) error {
	r.clearCalls++
	return nil
}

func TestCNProviderBalanceServiceRejectsMalformedHTTP200WithoutPersisting(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		body     string
	}{
		{name: "kimi business error", platform: PlatformKimi, body: `{"code":1001,"status":false,"scode":"invalid_authentication","data":{}}`},
		{name: "kimi missing balance", platform: PlatformKimi, body: `{"code":0,"status":true,"data":{}}`},
		{name: "kimi trailing garbage", platform: PlatformKimi, body: `{"code":0,"status":true,"data":{"available_balance":12.5}} trailing`},
		{name: "deepseek missing balance rows", platform: PlatformDeepseek, body: `{"is_available":true,"balance_infos":[]}`},
		{name: "deepseek invalid balance", platform: PlatformDeepseek, body: `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"not-a-number"}]}`},
		{name: "deepseek trailing garbage", platform: PlatformDeepseek, body: `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"12.5"}]} trailing`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := paygAccount(tt.platform)
			account.Schedulable = true
			repo := &cnBalanceStateRepo{account: account}
			svc := NewCNProviderBalanceService(repo, nil, &cnBalanceResponseUpstream{status: http.StatusOK, body: tt.body}, nil)

			result, err := svc.QueryBalance(context.Background(), account.ID)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.Success)
			require.NotEmpty(t, result.Error)
			require.False(t, result.Persisted)
			require.Empty(t, repo.updates, "invalid response must not replace the last known balance snapshot")
		})
	}
}

func TestCNProviderBalanceCheckDoesNotPauseOnMalformedHTTP200(t *testing.T) {
	account := paygAccount(PlatformKimi)
	account.Schedulable = true
	repo := &cnBalanceStateRepo{account: account}
	balanceSvc := NewCNProviderBalanceService(repo, nil, &cnBalanceResponseUpstream{
		status: http.StatusOK,
		body:   `{"code":1001,"status":false,"scode":"invalid_authentication","data":{}}`,
	}, nil)
	checkSvc := &CNProviderBalanceCheckService{accountRepo: repo, balanceService: balanceSvc}

	outcome := checkSvc.checkOne(context.Background(), account, 5)

	require.Equal(t, cnBalanceNoChange, outcome)
	require.Zero(t, repo.pauseCalls)
	require.Zero(t, repo.clearCalls)
}

func TestCNProviderBalanceServiceAcceptsValidUnavailableDeepseekBalance(t *testing.T) {
	account := paygAccount(PlatformDeepseek)
	repo := &cnBalanceStateRepo{account: account}
	svc := NewCNProviderBalanceService(repo, nil, &cnBalanceResponseUpstream{
		status: http.StatusOK,
		body:   `{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":"0.00"}]}`,
	}, nil)

	result, err := svc.QueryBalance(context.Background(), account.ID)

	require.NoError(t, err)
	require.True(t, result.Success, "is_available=false is valid provider state, not a malformed response")
	require.False(t, result.Available)
	require.Zero(t, result.Balance)
	require.True(t, result.Persisted)
	require.Len(t, repo.updates, 1)
}

func TestCNProviderBalanceServiceAcceptsValidKimiBalance(t *testing.T) {
	account := paygAccount(PlatformKimi)
	repo := &cnBalanceStateRepo{account: account}
	svc := NewCNProviderBalanceService(repo, nil, &cnBalanceResponseUpstream{
		status: http.StatusOK,
		body:   `{"code":0,"status":true,"scode":"","data":{"available_balance":12.5}}`,
	}, nil)

	result, err := svc.QueryBalance(context.Background(), account.ID)

	require.NoError(t, err)
	require.True(t, result.Success)
	require.True(t, result.Available)
	require.Equal(t, 12.5, result.Balance)
	require.Equal(t, "CNY", result.Currency)
	require.True(t, result.Persisted)
	require.Len(t, repo.updates, 1)
}

func TestCNProviderBalanceServiceRejectsBodyReadErrorWithoutPersisting(t *testing.T) {
	account := paygAccount(PlatformKimi)
	repo := &cnBalanceStateRepo{account: account}
	svc := NewCNProviderBalanceService(repo, nil, &cnBalanceResponseUpstream{
		status: http.StatusOK,
		bodyReader: &errorAfterDataReadCloser{data: []byte(
			`{"code":0,"status":true,"data":{"available_balance":12.5}}`,
		)},
	}, nil)

	result, err := svc.QueryBalance(context.Background(), account.ID)

	require.Error(t, err)
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	require.Nil(t, result)
	require.Empty(t, repo.updates, "a transport read error must not replace the last known balance snapshot")
}

func TestCNProviderQuotaServiceRejectsBodyReadErrorWithoutPersisting(t *testing.T) {
	account := codingAccount(PlatformKimi)
	repo := &cnBalanceStateRepo{account: account}
	svc := NewCNProviderQuotaService(repo, nil, &cnBalanceResponseUpstream{
		status: http.StatusOK,
		bodyReader: &errorAfterDataReadCloser{data: []byte(
			`{"usage":{"limit":100,"remaining":50}}`,
		)},
	}, nil)

	result, err := svc.QueryUsage(context.Background(), account.ID)

	require.Error(t, err)
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	require.Nil(t, result)
	require.Empty(t, repo.updates, "a transport read error must not replace the last known quota snapshot")
}

func TestCNProviderQuotaServiceRejectsMalformedOrEmptyHTTP200WithoutPersisting(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		body     string
	}{
		{name: "invalid json", platform: PlatformKimi, body: `{"usage":`},
		{name: "kimi missing tiers", platform: PlatformKimi, body: `{}`},
		{name: "kimi incomplete tier", platform: PlatformKimi, body: `{"usage":{}}`},
		{name: "zhipu missing tiers", platform: PlatformZhipu, body: `{"success":true,"data":{"limits":[]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := codingAccount(tt.platform)
			repo := &cnBalanceStateRepo{account: account}
			svc := NewCNProviderQuotaService(repo, nil, &cnBalanceResponseUpstream{
				status: http.StatusOK,
				body:   tt.body,
			}, nil)

			result, err := svc.QueryUsage(context.Background(), account.ID)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.Success)
			require.NotEmpty(t, result.Error)
			require.False(t, result.Persisted)
			require.Empty(t, repo.updates, "invalid response must not replace the last known quota snapshot")
		})
	}
}
