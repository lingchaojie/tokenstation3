package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ordinaryModelsUpstreamResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestFetchOpenAIModelsListUsesStandardRequestAndIsolatesCodexCache(t *testing.T) {
	var calls atomic.Int32
	s := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, concurrency int) (*http.Response, error) {
		calls.Add(1)
		assert.EqualValues(t, 2, accountID)
		assert.EqualValues(t, 3, concurrency)
		assert.Equal(t, "/v1/models", req.URL.Path)
		assert.Equal(t, "value", getHeaderRaw(req.Header, "x-account-header"))
		if req.URL.Query().Has("client_version") {
			return ordinaryModelsUpstreamResponse(`{"models":[{"slug":"codex-only"}]}`), nil
		}
		assert.Empty(t, req.Header.Get("Originator"))
		assert.Empty(t, req.Header.Get("Version"))
		return ordinaryModelsUpstreamResponse(`{"object":"list","data":[{"id":"special-upstream-model","owned_by":"provider","created":1234,"custom":true},{"id":"gpt-image-1"},{"id":"text-embedding-3-large"}]}`), nil
	}})
	account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	account.Credentials["header_overrides"] = map[string]any{"X-Account-Header": "value"}
	account.Credentials["header_override_enabled"] = true
	response, err := s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.Contains(t, string(response.Body), `"id":"gpt-image-1"`)
	require.Contains(t, string(response.Body), `"id":"text-embedding-3-large"`)
	require.Contains(t, string(response.Body), `"created":1234`)
	require.Contains(t, string(response.Body), `"custom":true`)
	manifest, err := s.FetchCodexModelsManifest(context.Background(), account, CodexCanonicalClientVersion(), "")
	require.NoError(t, err)
	require.Contains(t, string(manifest.Body), `"slug":"codex-only"`)
	again, err := s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, string(response.Body), string(again.Body))
	require.EqualValues(t, 2, calls.Load())
	account.Credentials["api_key"] = "rotated-token"
	_, err = s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.EqualValues(t, 3, calls.Load(), "credentials must participate in the cache key")
}

func TestFetchOpenAIModelsListOAuthSharesManifestCache(t *testing.T) {
	_, calls := newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"special-oauth-model","display_name":"Special OAuth Model","description":"manifest only"},{"slug":"gpt-image-1"}]}`)
	s := &OpenAIGatewayService{}
	account := newCodexModelsTestAccount()
	response, err := s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.JSONEq(t, `{"object":"list","data":[{"id":"special-oauth-model","object":"model","owned_by":"openai","created":0,"display_name":"Special OAuth Model"},{"id":"gpt-image-1","object":"model","owned_by":"openai","created":0}]}`, string(response.Body))
	manifest, err := s.FetchCodexModelsManifest(context.Background(), account, CodexCanonicalClientVersion(), "")
	require.NoError(t, err)
	require.Contains(t, string(manifest.Body), `"slug":"special-oauth-model"`)
	require.NotContains(t, string(manifest.Body), `"data"`)
	require.EqualValues(t, 1, calls.Load())
	_, err = s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
}

func TestFetchOpenAIModelsListCacheWindowsAndSingleflight(t *testing.T) {
	var calls atomic.Int32
	started := make(chan int32, 3)
	release := make(chan struct{}, 3)
	t.Cleanup(func() { close(release) })
	s := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		call := calls.Add(1)
		if call > 1 && call < 4 {
			started <- call
			<-release
		}
		if call >= 4 {
			response := ordinaryModelsUpstreamResponse(`{"error":"unavailable"}`)
			response.StatusCode = http.StatusServiceUnavailable
			return response, nil
		}
		return ordinaryModelsUpstreamResponse(`{"data":[{"id":"version-` + string(rune('0'+call)) + `"}]}`), nil
	}})
	account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	first, err := s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	for range 5 {
		_, err = s.FetchOpenAIModelsList(context.Background(), account)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, calls.Load())
	expireCodexModelsManifestCache(s, 2*time.Minute)
	stale, err := s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, string(first.Body), string(stale.Body))
	select {
	case call := <-started:
		require.EqualValues(t, 2, call)
	case <-time.After(2 * time.Second):
		t.Fatal("stale response did not start a background refresh")
	}
	for range 5 {
		cached, err := s.FetchOpenAIModelsList(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, string(first.Body), string(cached.Body))
	}
	require.EqualValues(t, 2, calls.Load(), "concurrent stale reads must share the refresh")
	release <- struct{}{}
	require.Eventually(t, func() bool {
		s.openAIModelsCache.mu.Lock()
		defer s.openAIModelsCache.mu.Unlock()
		for _, entry := range s.openAIModelsCache.entries {
			if strings.Contains(string(entry.manifest.Body), "version-2") {
				return true
			}
		}
		return false
	}, 2*time.Second, time.Millisecond)

	expireCodexModelsManifestCache(s, 6*time.Minute)
	responses := make(chan *OpenAIModelsResponse, 2)
	fetchErrors := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := s.FetchOpenAIModelsList(context.Background(), account)
			responses <- response
			fetchErrors <- err
		}()
	}
	select {
	case call := <-started:
		require.EqualValues(t, 3, call)
	case <-time.After(2 * time.Second):
		t.Fatal("expired catalog did not start a synchronous refresh")
	}
	select {
	case <-responses:
		t.Fatal("expired catalog returned before the upstream refresh completed")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	wait.Wait()
	for range 2 {
		require.NoError(t, <-fetchErrors)
		require.Contains(t, string((<-responses).Body), "version-3")
	}
	require.EqualValues(t, 3, calls.Load())
	expireCodexModelsManifestCache(s, 6*time.Minute)
	response, err := s.FetchOpenAIModelsList(context.Background(), account)
	require.Error(t, err)
	require.Nil(t, response)
	require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
}

func TestFetchOpenAIModelsListRevalidatesUpstreamETag(t *testing.T) {
	var calls atomic.Int32
	s := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		response := ordinaryModelsUpstreamResponse(`{"data":[{"id":"unchanged"}]}`)
		response.Header.Set("ETag", `"upstream"`)
		if calls.Add(1) > 1 {
			assert.Equal(t, `"upstream"`, req.Header.Get("If-None-Match"))
			response.StatusCode = http.StatusNotModified
		}
		return response, nil
	}})
	account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	first, err := s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	expireCodexModelsManifestCache(s, 2*time.Minute)
	_, err = s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		s.openAIModelsCache.mu.Lock()
		defer s.openAIModelsCache.mu.Unlock()
		for _, entry := range s.openAIModelsCache.entries {
			return entry.expiresAt.After(time.Now())
		}
		return false
	}, 2*time.Second, time.Millisecond)
	again, err := s.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, first.ETag, again.ETag)
	require.Equal(t, string(first.Body), string(again.Body))
	require.EqualValues(t, 2, calls.Load())
}

func TestProjectAccountModelsPassthroughIgnoresStaleMappings(t *testing.T) {
	account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	account.Extra = map[string]any{"openai_passthrough": true}
	account.Credentials["model_mapping"] = map[string]any{"obsolete": "missing"}
	body := []byte(`{"object":"list","data":[{"id":"live-model"}]}`)
	projected, err := projectAccountModelsBody(body, account, nil, false)
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(projected))
}

func TestProjectAccountModelsPassthroughFilterKeepsSourceNames(t *testing.T) {
	for _, codex := range []bool{false, true} {
		t.Run(fmt.Sprint(codex), func(t *testing.T) {
			account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
			account.Extra = map[string]any{"openai_passthrough": true, "openai_passthrough_model_filter": true}
			account.Credentials["model_mapping"] = map[string]any{
				"qwen3.8-max": "must-not-rewrite", "qwen3.8-*": "also-not-a-model", "gpt-6.2": "not-on-the-wire",
			}
			body := `{"object":"list","data":[{"id":"qwen3.8-max","display_name":"Qwen Max"},{"id":"qwen3.8-plus"},{"id":"gpt-6.1-sol"}]}`
			want := `{"object":"list","data":[{"id":"qwen3.8-max","display_name":"Qwen Max"},{"id":"qwen3.8-plus"}]}`
			if codex {
				body = strings.NewReplacer(`"data"`, `"models"`, `"id"`, `"slug"`).Replace(body)
				want = strings.NewReplacer(`"data"`, `"models"`, `"id"`, `"slug"`).Replace(want)
			}
			group := &Group{ModelsListConfig: GroupModelsListConfig{Enabled: true, Models: []string{"gpt-6.1-sol", "gpt-6.2", "qwen3.8-max", "qwen3.8-plus"}}}
			projected, err := projectAccountModelsBody([]byte(body), account, group, codex)
			require.NoError(t, err)
			require.JSONEq(t, want, string(projected))
		})
	}
}

func TestFetchOpenAIModelsListEmptyAndMalformedResponses(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":null}`, `{}`, `{"data":{}}`, `{"data":[{}]}`, `{"data":[null]}`} {
		t.Run(body, func(t *testing.T) {
			s := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				return ordinaryModelsUpstreamResponse(body), nil
			}})
			response, err := s.FetchOpenAIModelsList(context.Background(), newCodexModelsAPIKeyTestAccount("https://models.example/v1"))
			if body != `{"data":[]}` {
				require.Error(t, err)
				require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
				return
			}
			require.NoError(t, err)
			var result struct {
				Data []json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body, &result))
			require.NotNil(t, result.Data)
			require.Empty(t, result.Data)
		})
	}
}

func TestPinnedOpenAIModelsListMixedAccountsShareColdCacheAcrossGroups(t *testing.T) {
	_, oauthCalls := newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"shared-model"},{"slug":"oauth-special"}]}`)
	var apiCalls atomic.Int32
	s := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		apiCalls.Add(1)
		return ordinaryModelsUpstreamResponse(`{"data":[{"id":"shared-model","owned_by":"api-provider"},{"id":"api-special"}]}`), nil
	}})
	apiAccount := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	oauthAccount := newCodexModelsTestAccount()
	for _, account := range []*Account{apiAccount, oauthAccount} {
		account.Status, account.Schedulable = StatusActive, true
	}
	accounts := []Account{*apiAccount, *oauthAccount}
	s.accountRepo = splitCodexModelsAccountRepo{all: map[int64][]Account{10: accounts, 11: accounts}}
	groups := []*Group{
		{ID: 10, Platform: PlatformOpenAI, CodexModelsManifestConfig: GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{2, 1}},
			ModelsListConfig: GroupModelsListConfig{Enabled: true, Models: []string{"oauth-special", "shared-model"}}},
		{ID: 11, Platform: PlatformOpenAI, CodexModelsManifestConfig: GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{2, 1}}},
	}
	type result struct {
		response *OpenAIModelsResponse
		account  *Account
		err      error
	}
	results := make([]result, len(groups))
	var wait sync.WaitGroup
	for i, group := range groups {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[i].response, results[i].account, results[i].err = s.FetchPinnedOpenAIModelsList(context.Background(), group, 3, "")
		}()
	}
	wait.Wait()
	for i, result := range results {
		require.NoError(t, result.err)
		require.EqualValues(t, 2, result.account.ID)
		var catalog struct {
			Data []struct {
				ID    string `json:"id"`
				Owner string `json:"owned_by"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(result.response.Body, &catalog))
		ids := make([]string, 0, len(catalog.Data))
		for _, model := range catalog.Data {
			ids = append(ids, model.ID)
			if model.ID == "shared-model" {
				require.Equal(t, "api-provider", model.Owner)
			}
		}
		if i == 0 {
			require.Equal(t, []string{"oauth-special", "shared-model"}, ids)
		} else {
			require.Equal(t, []string{"shared-model", "api-special", "oauth-special"}, ids)
		}
	}
	require.EqualValues(t, 1, apiCalls.Load())
	require.EqualValues(t, 1, oauthCalls.Load())
}

func TestFetchOpenAIModelsListResolvesShadowOAuthCredentials(t *testing.T) {
	_, calls := newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"parent-model"}]}`)
	parent := newCodexModelsTestAccount()
	shadow := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID}
	s := &OpenAIGatewayService{accountRepo: newStubCredRepo(parent)}
	response, err := s.FetchOpenAIModelsList(context.Background(), shadow)
	require.NoError(t, err)
	require.Contains(t, string(response.Body), `"id":"parent-model"`)
	require.EqualValues(t, 1, calls.Load())
}

type scopedModelCatalogParentRepo struct {
	AccountRepository
	parent  *Account
	lookups int
}

func (r *scopedModelCatalogParentRepo) GetByID(context.Context, int64) (*Account, error) {
	r.lookups++
	return r.parent, nil
}

func TestFetchOpenAIModelsListChecksScopedShadowCredentialOwner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		groups []int64
		allow  bool
	}{
		{name: "only shadow allowed", groups: []int64{1}},
		{name: "shadow and parent allowed", groups: []int64{1, 2}, allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, calls := newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"parent-model"}]}`)
			parent := newCodexModelsTestAccount()
			parent.GroupIDs = []int64{2}
			shadow := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, GroupIDs: []int64{1}}
			repo := &scopedModelCatalogParentRepo{parent: parent}
			s := &OpenAIGatewayService{accountRepo: repo}
			ctx := adminaccess.WithPrincipal(t.Context(), &adminaccess.Principal{Policy: adminaccess.Policy{
				Rules: []adminaccess.Rule{{Permissions: []string{"accounts.read"}, GroupIDs: tc.groups, AccountTypes: []string{AccountTypeOAuth}}},
			}})
			result, err := s.FetchOpenAIModelsList(ctx, shadow)
			if tc.allow {
				require.NoError(t, err)
				require.Contains(t, string(result.Body), `"id":"parent-model"`)
				require.EqualValues(t, 1, calls.Load())
			} else {
				require.Equal(t, "SCOPED_ACCOUNT_NOT_FOUND", infraerrors.Reason(err))
				require.Nil(t, result)
				require.Zero(t, calls.Load())
			}
			require.Equal(t, 1, repo.lookups, "catalog retrieval must use the credential account already checked")
		})
	}
}

type scopedModelCatalogStateRepository struct {
	codexModelsAccountStateRepo
	parent  *Account
	lookups int
}

func (r *scopedModelCatalogStateRepository) GetByID(context.Context, int64) (*Account, error) {
	r.lookups++
	return r.parent, nil
}

func TestFetchOpenAIModelsListScopedOAuth401LeavesAccountStateUntouched(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"token_revoked"}}`))
	}))
	t.Cleanup(server.Close)
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	t.Cleanup(func() { chatgptCodexModelsURL = original })

	for _, tc := range []struct {
		name             string
		scoped           bool
		wantHealthWrites int
		wantOwnerReads   int
	}{
		{name: "scoped catalog", scoped: true, wantOwnerReads: 1},
		{name: "legacy admin catalog", wantHealthWrites: 1, wantOwnerReads: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := newCodexModelsTestAccount()
			parent.GroupIDs = []int64{2}
			shadow := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, GroupIDs: []int64{1}}
			repo := &scopedModelCatalogStateRepository{parent: parent}
			s := newCodexModels401TestService(repo)
			s.accountRepo = repo

			ctx := t.Context()
			if tc.scoped {
				ctx = adminaccess.WithPrincipal(ctx, &adminaccess.Principal{Policy: adminaccess.Policy{
					Rules: []adminaccess.Rule{{Permissions: []string{"accounts.read"}, GroupIDs: []int64{1, 2}, AccountTypes: []string{AccountTypeOAuth}}},
				}})
			}
			before := calls.Load()
			_, err := s.FetchOpenAIModelsList(ctx, shadow)
			require.Equal(t, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", infraerrors.Reason(err))
			assert.EqualValues(t, before+1, calls.Load())
			assert.Equal(t, tc.wantHealthWrites, repo.setErrorCalls)
			assert.Zero(t, repo.setTempUnschedCalls)
			assert.Equal(t, tc.wantOwnerReads, repo.lookups)
			assert.Equal(t, tc.wantHealthWrites > 0, s.isOpenAIAccountRuntimeBlocked(parent))
		})
	}
}

func TestFetchOpenAIModelsListScopedAgentIdentityUsesOnlyExistingTask(t *testing.T) {
	for _, taskID := range []string{"", "existing-task"} {
		t.Run("task="+taskID, func(t *testing.T) {
			var modelsCalls, registerCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/task/register") {
					registerCalls.Add(1)
					_, _ = w.Write([]byte(`{"task_id":"replacement-task"}`))
					return
				}
				modelsCalls.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_task_id"}}`))
			}))
			t.Cleanup(server.Close)
			originalModelsURL := chatgptCodexModelsURL
			chatgptCodexModelsURL = server.URL
			t.Cleanup(func() { chatgptCodexModelsURL = originalModelsURL })
			originalAuthURL := openAIAgentIdentityAuthAPIBaseURL
			openAIAgentIdentityAuthAPIBaseURL = server.URL
			t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = originalAuthURL })

			key, privateKey := newTestAgentIdentityKey(t)
			account := &Account{
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, GroupIDs: []int64{1},
				Credentials: map[string]any{
					"auth_mode":         OpenAIAuthModeAgentIdentity,
					"agent_runtime_id":  key.runtimeID,
					"agent_private_key": privateKey,
					"task_id":           taskID,
				},
			}
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			s := &OpenAIGatewayService{accountRepo: repo}
			ctx := adminaccess.WithPrincipal(t.Context(), &adminaccess.Principal{Policy: adminaccess.Policy{
				Rules: []adminaccess.Rule{{Permissions: []string{"accounts.read"}, GroupIDs: []int64{1}, AccountTypes: []string{AccountTypeOAuth}}},
			}})

			_, err := s.FetchOpenAIModelsList(ctx, account)
			if taskID == "" {
				assert.Equal(t, "OPENAI_CODEX_MODELS_AUTH_FAILED", infraerrors.Reason(err))
				assert.Zero(t, modelsCalls.Load())
			} else {
				assert.Equal(t, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", infraerrors.Reason(err))
				assert.EqualValues(t, 1, modelsCalls.Load())
			}
			assert.Zero(t, registerCalls.Load())
			assert.Equal(t, taskID, account.GetCredential("task_id"))
		})
	}
}

func TestProjectAccountModelsCannotExposeCodexMediaThroughAlias(t *testing.T) {
	account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	account.Credentials["model_mapping"] = map[string]any{"image-alias": "gpt-image-1", "auto-alias": "codex-auto-fast"}
	body := []byte(`{"models":[{"slug":"gpt-image-1"},{"slug":"codex-auto-fast"}]}`)
	projected, err := projectAccountModelsBody(body, account, &Group{}, true)
	require.NoError(t, err)
	require.JSONEq(t, `{"models":[]}`, string(projected))
}

func TestFetchOpenAIModelsListRejectsUnexpectedCold304(t *testing.T) {
	s := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotModified, Header: make(http.Header), Body: http.NoBody}, nil
	}})
	response, err := s.FetchOpenAIModelsList(context.Background(), newCodexModelsAPIKeyTestAccount("https://models.example/v1"))
	require.Error(t, err)
	require.Nil(t, response)
	require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
}

func TestOpenAIModelsCacheSeparatesRepresentationsForIdenticalRequests(t *testing.T) {
	s := &OpenAIGatewayService{}
	request := openAIModelsRequest{url: "https://models.example/v1/models", accountID: 7}
	var calls atomic.Int32
	fetch := func(body string) func(context.Context, string) (*OpenAIModelsResponse, error) {
		return func(context.Context, string) (*OpenAIModelsResponse, error) {
			calls.Add(1)
			return &OpenAIModelsResponse{Body: []byte(body)}, nil
		}
	}
	manifestBody := `{"models":[{"slug":"shared"}]}`
	listBody := `{"object":"list","data":[{"id":"shared"}]}`
	_, err := s.fetchCachedOpenAIModels(context.Background(), request, fetch(manifestBody), "")
	require.NoError(t, err)
	request.standardModelsList = true
	list, err := s.fetchCachedOpenAIModels(context.Background(), request, fetch(listBody), "")
	require.NoError(t, err)
	require.JSONEq(t, listBody, string(list.Body))
	request.standardModelsList = false
	manifest, err := s.fetchCachedOpenAIModels(context.Background(), request, fetch(manifestBody), "")
	require.NoError(t, err)
	require.JSONEq(t, manifestBody, string(manifest.Body))
	require.EqualValues(t, 2, calls.Load())
}

func TestApplyCodexModelsMappingPreservesUnchangedBodyAndETag(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(fmt.Sprint(pinned), func(t *testing.T) {
			account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
			account.Credentials["model_mapping"] = map[string]any{"gpt-5.4": "gpt-5.4"}
			body := []byte(`{ "unknown": { "keep": true }, "models": [ { "slug": "gpt-5.4", "custom": 123 } ] }`)
			response := &OpenAIModelsResponse{Body: body, ETag: `W/"upstream-validator"`}
			group := &Group{Platform: PlatformOpenAI, CodexModelsManifestConfig: GroupCodexModelsManifestConfig{Enabled: pinned}}
			require.NoError(t, ApplyPinnedCodexModelsMapping(response, account, group))
			require.Equal(t, body, response.Body)
			require.Equal(t, `W/"upstream-validator"`, response.ETag)
		})
	}
}
