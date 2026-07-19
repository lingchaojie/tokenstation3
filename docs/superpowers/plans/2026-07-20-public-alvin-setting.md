# Public Alvin Setting Endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an unauthenticated `GET /api/v1/settings/alvin` endpoint that reads the fixed `alvin` key from the existing `settings` table and returns it as a boolean in the standard API envelope.

**Architecture:** Keep this feature isolated from the full public-settings payload. A dedicated `SettingService.GetAlvin(context.Context) (bool, error)` reads the fixed key on every request, strictly parses `true`/`false`, and falls back to `true` only for missing or invalid values. Startup initialization creates `alvin=true` for new and existing installations without overwriting an existing row, while a public Gin handler returns a typed payload with `Cache-Control: no-store` outside the JWT route group.

**Review amendment:** Startup initialization must use an atomic repository `SetIfAbsent` implemented as `INSERT ... ON CONFLICT (key) DO NOTHING`. Do not put `alvin` in the existing bulk-upsert defaults map and do not use a read-then-upsert sequence; both can overwrite a value inserted before or during startup. This amendment supersedes the Task 2 snippets below where they differ.

**Tech Stack:** Go 1.26.5, Gin, ent-backed `SettingRepository`, `testify/require`, Go build-tagged unit tests.

## Global Constraints

- The route is exactly `GET /api/v1/settings/alvin` and requires neither JWT nor API Key authentication.
- The success body uses the standard envelope with `data.alvin` as a JSON boolean: `{"code":0,"message":"success","data":{"alvin":true}}`.
- The database key is fixed as `alvin`; no caller-supplied key or generic public setting lookup is allowed.
- Canonical values are the strings `true` and `false`. Parsing ignores surrounding whitespace and letter case. Missing, empty, or invalid values fall back to `true`.
- Repository failures other than `service.ErrSettingNotFound` return HTTP 500 through the existing error envelope.
- Startup never overwrites an existing `alvin` row. It creates a missing row with `true` for fresh and upgraded databases.
- The read path queries the database on every request and adds no Redis or process-local cache.
- Do not add a schema migration, frontend/admin UI, write endpoint, HTML injection, or field on `/api/v1/settings/public`.
- Preserve unrelated tracked and untracked working-tree changes.

---

## File Structure

- Modify `backend/internal/service/domain_constants.go`: own the fixed `SettingKeyAlvin` name.
- Modify `backend/internal/service/setting_service.go`: own strict boolean resolution and idempotent default initialization.
- Create `backend/internal/service/setting_service_alvin_test.go`: own parsing, repository-error, and startup-default service tests.
- Modify `backend/internal/handler/dto/settings.go`: own the typed public response.
- Modify `backend/internal/handler/setting_handler.go`: own HTTP response, error, and cache-header behavior.
- Create `backend/internal/handler/setting_handler_alvin_test.go`: own handler contract tests.
- Modify `backend/internal/server/routes/auth.go`: register the endpoint in the unauthenticated settings group.
- Create `backend/internal/server/routes/alvin_route_test.go`: prove the route bypasses JWT middleware.

---

### Task 1: Add the dedicated setting reader

**Files:**
- Modify: `backend/internal/service/domain_constants.go:302-318`
- Modify: `backend/internal/service/setting_service.go:701-777`
- Create: `backend/internal/service/setting_service_alvin_test.go`

**Interfaces:**
- Consumes: `SettingRepository.GetValue(ctx context.Context, key string) (string, error)` and `ErrSettingNotFound`.
- Produces: `SettingKeyAlvin = "alvin"`, `defaultAlvinValue = true`, and `func (s *SettingService) GetAlvin(ctx context.Context) (bool, error)`.

- [ ] **Step 1: Create the focused repository stub and failing reader tests**

Create `backend/internal/service/setting_service_alvin_test.go`:

~~~go
//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type alvinSettingRepoStub struct {
	SettingRepository
	values           map[string]string
	getValueErrors   map[string]error
	setErrors        map[string]error
	setMultipleError error
}

func newAlvinSettingRepoStub(values map[string]string) *alvinSettingRepoStub {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return &alvinSettingRepoStub{
		values:         cloned,
		getValueErrors: make(map[string]error),
		setErrors:      make(map[string]error),
	}
}

func (r *alvinSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if err := r.getValueErrors[key]; err != nil {
		return "", err
	}
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *alvinSettingRepoStub) Set(_ context.Context, key, value string) error {
	if err := r.setErrors[key]; err != nil {
		return err
	}
	r.values[key] = value
	return nil
}

func (r *alvinSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (r *alvinSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	if r.setMultipleError != nil {
		return r.setMultipleError
	}
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func TestSettingService_GetAlvin(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   bool
	}{
		{name: "true", values: map[string]string{SettingKeyAlvin: "true"}, want: true},
		{name: "false", values: map[string]string{SettingKeyAlvin: "false"}, want: false},
		{name: "trimmed uppercase true", values: map[string]string{SettingKeyAlvin: "  TRUE  "}, want: true},
		{name: "trimmed mixed case false", values: map[string]string{SettingKeyAlvin: "  FaLsE  "}, want: false},
		{name: "missing", values: map[string]string{}, want: true},
		{name: "empty", values: map[string]string{SettingKeyAlvin: ""}, want: true},
		{name: "numeric is invalid", values: map[string]string{SettingKeyAlvin: "1"}, want: true},
		{name: "arbitrary text is invalid", values: map[string]string{SettingKeyAlvin: "disabled"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newAlvinSettingRepoStub(tt.values)
			svc := NewSettingService(repo, &config.Config{})

			got, err := svc.GetAlvin(context.Background())

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSettingService_GetAlvin_ReturnsRepositoryError(t *testing.T) {
	repo := newAlvinSettingRepoStub(nil)
	dbErr := errors.New("database unavailable")
	repo.getValueErrors[SettingKeyAlvin] = dbErr
	svc := NewSettingService(repo, &config.Config{})

	_, err := svc.GetAlvin(context.Background())

	require.ErrorIs(t, err, dbErr)
}
~~~

- [ ] **Step 2: Run the tests and verify the missing contract fails**

Run:

~~~bash
cd backend
go test -tags=unit ./internal/service -run 'TestSettingService_GetAlvin' -count=1
~~~

Expected: compilation fails because `SettingKeyAlvin` and `(*SettingService).GetAlvin` do not exist.

- [ ] **Step 3: Add the fixed key constant**

Add beside the other public/OEM keys in `backend/internal/service/domain_constants.go`:

~~~go
	SettingKeyAlvin = "alvin" // 外部项目读取的公开布尔变量
~~~

- [ ] **Step 4: Implement strict uncached boolean resolution**

Add immediately before `GetPublicSettings` in `backend/internal/service/setting_service.go`:

~~~go
const defaultAlvinValue = true

// GetAlvin returns the public alvin flag directly from the settings table.
// Missing or invalid values use the documented true default.
func (s *SettingService) GetAlvin(ctx context.Context) (bool, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAlvin)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaultAlvinValue, nil
		}
		return false, fmt.Errorf("get alvin setting: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return defaultAlvinValue, nil
	}
}
~~~

Do not add `alvin` to `GetPublicSettings`, an injection payload, or any application cache.

- [ ] **Step 5: Format and rerun the reader tests**

Run from the repository root:

~~~bash
gofmt -w backend/internal/service/domain_constants.go backend/internal/service/setting_service.go backend/internal/service/setting_service_alvin_test.go
cd backend
go test -tags=unit ./internal/service -run 'TestSettingService_GetAlvin' -count=1
~~~

Expected: both reader tests pass.

- [ ] **Step 6: Commit the reader**

~~~bash
git add backend/internal/service/domain_constants.go backend/internal/service/setting_service.go backend/internal/service/setting_service_alvin_test.go
git commit -m "feat(settings): add alvin setting reader"
~~~

---

### Task 2: Backfill the default without overwriting existing values

**Files:**
- Modify: `backend/internal/service/setting_service_alvin_test.go`
- Modify: `backend/internal/service/setting_service.go:3341-3572`

**Interfaces:**
- Consumes: `SettingKeyAlvin`, `defaultAlvinValue`, and `alvinSettingRepoStub` from Task 1.
- Produces: `func (s *SettingService) ensureAlvinDefault(ctx context.Context) error` and startup behavior that only creates a missing `alvin` row.

- [ ] **Step 1: Add failing startup initialization tests**

Append to `backend/internal/service/setting_service_alvin_test.go`:

~~~go
func TestSettingService_InitializeDefaultSettings_SeedsAlvinForNewDatabase(t *testing.T) {
	repo := newAlvinSettingRepoStub(nil)
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, "true", repo.values[SettingKeyAlvin])
}

func TestSettingService_InitializeDefaultSettings_BackfillsAlvinForExistingDatabase(t *testing.T) {
	repo := newAlvinSettingRepoStub(map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "Custom Portal",
		SettingKeySiteSubtitle:        "Custom subtitle",
	})
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, "true", repo.values[SettingKeyAlvin])
}

func TestSettingService_InitializeDefaultSettings_PreservesExistingAlvin(t *testing.T) {
	repo := newAlvinSettingRepoStub(map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeySiteName:            "Custom Portal",
		SettingKeySiteSubtitle:        "Custom subtitle",
		SettingKeyAlvin:               "false",
	})
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, "false", repo.values[SettingKeyAlvin])
}

func TestSettingService_InitializeDefaultSettings_ReturnsAlvinCheckError(t *testing.T) {
	repo := newAlvinSettingRepoStub(map[string]string{
		SettingKeyRegistrationEnabled: "true",
	})
	dbErr := errors.New("alvin lookup failed")
	repo.getValueErrors[SettingKeyAlvin] = dbErr
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.ErrorIs(t, err, dbErr)
}

func TestSettingService_InitializeDefaultSettings_ReturnsAlvinBackfillError(t *testing.T) {
	repo := newAlvinSettingRepoStub(map[string]string{
		SettingKeyRegistrationEnabled: "true",
	})
	dbErr := errors.New("alvin insert failed")
	repo.setErrors[SettingKeyAlvin] = dbErr
	svc := NewSettingService(repo, &config.Config{})

	err := svc.InitializeDefaultSettings(context.Background())

	require.ErrorIs(t, err, dbErr)
}
~~~

- [ ] **Step 2: Run the initialization tests and verify the upgrade cases fail**

~~~bash
cd backend
go test -tags=unit ./internal/service -run 'TestSettingService_InitializeDefaultSettings_.*Alvin' -count=1
~~~

Expected: fresh-database and existing-database assertions fail because `alvin` is not initialized; error-path tests fail because no `alvin` lookup occurs.

- [ ] **Step 3: Add the idempotent backfill helper**

Add immediately before `InitializeDefaultSettings`:

~~~go
func (s *SettingService) ensureAlvinDefault(ctx context.Context) error {
	_, err := s.settingRepo.GetValue(ctx, SettingKeyAlvin)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrSettingNotFound) {
		return fmt.Errorf("check alvin setting: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAlvin, strconv.FormatBool(defaultAlvinValue)); err != nil {
		return fmt.Errorf("initialize alvin setting: %w", err)
	}
	return nil
}
~~~

- [ ] **Step 4: Invoke the helper for existing installations**

Replace the existing early-return branch at the start of `InitializeDefaultSettings` with:

~~~go
	_, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEnabled)
	if err == nil {
		if err := s.ensureAlvinDefault(ctx); err != nil {
			return err
		}
		// 已有设置时仍要迁移旧品牌默认值；管理员自定义值不覆盖。
		return s.migrateLegacyBrandingDefaults(ctx)
	}
~~~

Leave the following non-`ErrSettingNotFound` branch unchanged.

- [ ] **Step 5: Seed fresh databases in the existing defaults map**

Add beside the other public/OEM defaults:

~~~go
		SettingKeyAlvin: strconv.FormatBool(defaultAlvinValue),
~~~

Do not call `ensureAlvinDefault` after the bulk insert; the defaults map is the single write path for a fresh database.

- [ ] **Step 6: Format and run initialization, reader, and branding regression tests**

~~~bash
gofmt -w backend/internal/service/setting_service.go backend/internal/service/setting_service_alvin_test.go
cd backend
go test -tags=unit ./internal/service -run 'TestSettingService_(GetAlvin|InitializeDefaultSettings_.*Alvin|InitializeDefaultSettings_.*Branding)' -count=1
~~~

Expected: all selected tests pass, including preservation of `alvin=false` and existing branding migration behavior.

- [ ] **Step 7: Commit startup initialization**

~~~bash
git add backend/internal/service/setting_service.go backend/internal/service/setting_service_alvin_test.go
git commit -m "feat(settings): initialize alvin default"
~~~

---

### Task 3: Expose and verify the unauthenticated HTTP endpoint

**Files:**
- Modify: `backend/internal/handler/dto/settings.go:315-320`
- Modify: `backend/internal/handler/setting_handler.go:34-124`
- Create: `backend/internal/handler/setting_handler_alvin_test.go`
- Modify: `backend/internal/server/routes/auth.go:213-219`
- Create: `backend/internal/server/routes/alvin_route_test.go`

**Interfaces:**
- Consumes: `func (s *SettingService) GetAlvin(ctx context.Context) (bool, error)` from Task 1.
- Produces: `dto.AlvinSettingResponse`, `func (h *SettingHandler) GetAlvin(c *gin.Context)`, and registered route `GET /api/v1/settings/alvin`.

- [ ] **Step 1: Write failing handler contract tests**

Create `backend/internal/handler/setting_handler_alvin_test.go`:

~~~go
//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type alvinHandlerSettingRepo struct {
	service.SettingRepository
	value string
	err   error
}

func (r *alvinHandlerSettingRepo) GetValue(_ context.Context, _ string) (string, error) {
	return r.value, r.err
}

func TestSettingHandler_GetAlvin_ReturnsStandardEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &alvinHandlerSettingRepo{value: "false"}
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), "test-version")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/alvin", nil)

	h.GetAlvin(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"code":0,"message":"success","data":{"alvin":false}}`, recorder.Body.String())
}

func TestSettingHandler_GetAlvin_ReturnsInternalErrorForDatabaseFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &alvinHandlerSettingRepo{err: errors.New("database unavailable")}
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), "test-version")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/alvin", nil)

	h.GetAlvin(c)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	var body struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, http.StatusInternalServerError, body.Code)
}
~~~

- [ ] **Step 2: Run the handler tests and verify the missing handler fails**

~~~bash
cd backend
go test -tags=unit ./internal/handler -run 'TestSettingHandler_GetAlvin' -count=1
~~~

Expected: compilation fails because `(*SettingHandler).GetAlvin` does not exist.

- [ ] **Step 3: Add the typed DTO and handler**

Add immediately before `PublicSettings` in `backend/internal/handler/dto/settings.go`:

~~~go
type AlvinSettingResponse struct {
	Alvin bool `json:"alvin"`
}
~~~

Add immediately after `GetPublicSettings` in `backend/internal/handler/setting_handler.go`:

~~~go
// GetAlvin returns the public alvin boolean setting.
// GET /api/v1/settings/alvin
func (h *SettingHandler) GetAlvin(c *gin.Context) {
	alvin, err := h.settingService.GetAlvin(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	c.Header("Cache-Control", "no-store")
	response.Success(c, dto.AlvinSettingResponse{Alvin: alvin})
}
~~~

- [ ] **Step 4: Format and rerun the handler tests**

~~~bash
gofmt -w backend/internal/handler/dto/settings.go backend/internal/handler/setting_handler.go backend/internal/handler/setting_handler_alvin_test.go
cd backend
go test -tags=unit ./internal/handler -run 'TestSettingHandler_GetAlvin' -count=1
~~~

Expected: both handler tests pass; the success body contains a JSON boolean and `Cache-Control: no-store`.

- [ ] **Step 5: Write the failing public-route test**

Create `backend/internal/server/routes/alvin_route_test.go`:

~~~go
package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type alvinRouteSettingRepo struct {
	service.SettingRepository
}

func (r *alvinRouteSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyAlvin {
		return "false", nil
	}
	return "", service.ErrSettingNotFound
}

func TestAuthRoutesExposeAlvinWithoutJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settingService := service.NewSettingService(&alvinRouteSettingRepo{}, &config.Config{})
	settingHandler := handler.NewSettingHandler(settingService, "test-version")
	router := gin.New()
	v1 := router.Group("/api/v1")
	jwtCalls := 0
	jwtAuth := servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
		jwtCalls++
		c.AbortWithStatus(http.StatusUnauthorized)
	})

	RegisterAuthRoutes(v1, &handler.Handlers{
		Auth:    &handler.AuthHandler{},
		Setting: settingHandler,
	}, jwtAuth, nil, settingService)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/alvin", nil)
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Zero(t, jwtCalls)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"code":0,"message":"success","data":{"alvin":false}}`, recorder.Body.String())
}
~~~

- [ ] **Step 6: Run the route test and verify the route is absent**

~~~bash
cd backend
go test ./internal/server/routes -run 'TestAuthRoutesExposeAlvinWithoutJWT' -count=1
~~~

Expected: FAIL because the request returns HTTP 404 instead of HTTP 200.

- [ ] **Step 7: Register the route outside JWT authentication**

Add the Alvin route to the existing public settings group in `backend/internal/server/routes/auth.go`:

~~~go
	settings := v1.Group("/settings")
	{
		settings.GET("/public", h.Setting.GetPublicSettings)
		settings.GET("/alvin", h.Setting.GetAlvin)
		settings.GET("/model-pricing", h.Setting.GetPublicModelPricing)
		settings.GET("/email-unsubscribe", h.Setting.UnsubscribeNotificationEmail)
	}
~~~

- [ ] **Step 8: Format and run every targeted Alvin test**

~~~bash
gofmt -w backend/internal/server/routes/auth.go backend/internal/server/routes/alvin_route_test.go
cd backend
go test -tags=unit ./internal/service -run 'TestSettingService_(GetAlvin|InitializeDefaultSettings_.*Alvin)' -count=1
go test -tags=unit ./internal/handler -run 'TestSettingHandler_GetAlvin' -count=1
go test ./internal/server/routes -run 'TestAuthRoutesExposeAlvinWithoutJWT' -count=1
~~~

Expected: all selected service, handler, and route tests pass.

- [ ] **Step 9: Run the complete backend unit suite**

~~~bash
cd backend
go test -tags=unit ./...
~~~

Expected: every backend unit test passes with zero failures.

- [ ] **Step 10: Verify scope and generated-file cleanliness**

Run from the repository root:

~~~bash
git diff --check HEAD~2
git status --short
git diff HEAD~2 -- backend/internal/service/domain_constants.go backend/internal/service/setting_service.go backend/internal/service/setting_service_alvin_test.go backend/internal/handler/dto/settings.go backend/internal/handler/setting_handler.go backend/internal/handler/setting_handler_alvin_test.go backend/internal/server/routes/auth.go backend/internal/server/routes/alvin_route_test.go
~~~

Expected: `git diff --check HEAD~2` exits 0. Because Tasks 1 and 2 each created one commit, `HEAD~2` is the feature base at this checkpoint; the diff therefore contains the first two commits plus Task 3's working-tree changes. It contains only the fixed key, reader/default initialization, typed handler, public route, and their tests. Existing unrelated untracked documents remain untouched.

- [ ] **Step 11: Commit the public endpoint**

~~~bash
git add backend/internal/handler/dto/settings.go backend/internal/handler/setting_handler.go backend/internal/handler/setting_handler_alvin_test.go backend/internal/server/routes/auth.go backend/internal/server/routes/alvin_route_test.go
git commit -m "feat(api): expose public alvin setting"
~~~

---

## Post-implementation Contract Check

After starting the backend against a test database, call the endpoint without credentials:

~~~bash
curl --fail --silent http://127.0.0.1:8080/api/v1/settings/alvin
~~~

Expected initial body:

~~~json
{"code":0,"message":"success","data":{"alvin":true}}
~~~

Change only the test database row:

~~~sql
UPDATE settings
SET value = 'false', updated_at = NOW()
WHERE key = 'alvin';
~~~

Repeat the unauthenticated request. Expected body:

~~~json
{"code":0,"message":"success","data":{"alvin":false}}
~~~
