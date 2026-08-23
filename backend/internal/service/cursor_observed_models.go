package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const (
	cursorObservedModelsExtraKey = "cursor_observed_models"
	cursorObservedModelsTTL      = 6 * time.Hour
	cursorObservedModelsTimeout  = 15 * time.Second

	cursorAvailableModelsResponseLimit = 1 << 20
)

type cursorObservedModelsSnapshot struct {
	Models    []string `json:"models"`
	FetchedAt string   `json:"fetched_at"`
	Source    string   `json:"source,omitempty"`
}

// CursorObservedModelsService periodically refreshes the raw api2
// AvailableModels snapshot for enabled Cursor OAuth accounts. Wiring is kept
// separate from construction so server lifecycle ownership can start and stop
// it with the other background services.
type CursorObservedModelsService struct {
	accountRepo   AccountRepository
	tokenProvider *CursorTokenProvider
	httpUpstream  HTTPUpstream
	interval      time.Duration
	timeout       time.Duration
	cfg           *config.Config

	runCtx    context.Context
	runCancel context.CancelFunc
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

func NewCursorObservedModelsService(
	accountRepo AccountRepository,
	tokenProvider *CursorTokenProvider,
	httpUpstream HTTPUpstream,
	interval time.Duration,
	configs ...*config.Config,
) *CursorObservedModelsService {
	if interval <= 0 {
		interval = cursorObservedModelsTTL
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	service := &CursorObservedModelsService{
		accountRepo: accountRepo, tokenProvider: tokenProvider, httpUpstream: httpUpstream,
		interval: interval, timeout: cursorObservedModelsTimeout,
		runCtx: runCtx, runCancel: runCancel,
	}
	if len(configs) > 0 {
		service.cfg = configs[0]
	}
	return service
}

func (s *CursorObservedModelsService) Start() {
	if s == nil || s.accountRepo == nil || s.httpUpstream == nil || s.interval <= 0 {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(s.interval)
			defer ticker.Stop()

			s.runBackgroundOnce()
			for {
				select {
				case <-ticker.C:
					s.runBackgroundOnce()
				case <-s.runCtx.Done():
					return
				}
			}
		}()
	})
}

func (s *CursorObservedModelsService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.runCancel != nil {
			s.runCancel()
		}
	})
	s.wg.Wait()
}

func (s *CursorObservedModelsService) runBackgroundOnce() {
	ctx := s.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Debug("cursor_observed_models_sync_failed", "error", err)
	}
}

func (s *CursorObservedModelsService) runOnce(ctx context.Context) error {
	if s == nil || s.accountRepo == nil || s.httpUpstream == nil {
		return errors.New("cursor observed models service is not configured")
	}
	accounts, err := s.accountRepo.ListSchedulableByPlatform(ctx, PlatformCursor)
	if err != nil {
		return err
	}
	var syncErrors []error
	for i := range accounts {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(syncErrors, err)...)
		}
		account := &accounts[i]
		if !cursorObservedModelsAccountEligible(account) {
			continue
		}
		timeout := s.timeout
		if timeout <= 0 {
			timeout = cursorObservedModelsTimeout
		}
		accountCtx, cancel := context.WithTimeout(ctx, timeout)
		err := s.syncAccount(accountCtx, account)
		cancel()
		if err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("cursor account %d: %w", account.ID, err))
		}
	}
	return errors.Join(syncErrors...)
}

func cursorObservedModelsAccountEligible(account *Account) bool {
	return account != nil && account.Platform == PlatformCursor && account.Type == AccountTypeOAuth &&
		account.Status == StatusActive && account.Schedulable
}

func (s *CursorObservedModelsService) syncAccount(ctx context.Context, account *Account) error {
	if account == nil {
		return errors.New("cursor account is nil")
	}
	if cursorObservedModelsFresh(account.Extra, time.Now()) {
		return nil
	}

	token := ""
	if s.tokenProvider != nil {
		resolved, err := s.tokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(resolved)
	} else {
		token = strings.TrimSpace(account.GetCursorAccessToken())
	}
	if token == "" {
		return errors.New("cursor access token is unavailable")
	}
	if cursorpkg.IsWebSessionToken(token) {
		return errCursorWebSessionNotUpgraded
	}

	proxyURL, err := cursorResolvedProxyURL(account, time.Now())
	if err != nil {
		return err
	}
	targetURL, err := cursorAvailableModelsURL(account.GetCursorBaseURL(), s.cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		targetURL,
		bytes.NewReader(cursorpkg.EncodeAvailableModelsRequest(false, false)),
	)
	if err != nil {
		return err
	}
	copyCursorHeaders(req.Header, cursorpkg.BuildHeaders(token, cursorpkg.ContentTypeProto))
	account.ApplyHeaderOverrides(req.Header)

	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Cursor AvailableModels returned HTTP %d", resp.StatusCode)
	}
	body, err := readCursorAvailableModelsBody(resp.Body)
	if err != nil {
		return err
	}
	models, err := cursorpkg.ParseAvailableModelsResponse(body)
	if err != nil {
		return err
	}
	ids := cursorObservedModelIDs(models)
	if len(ids) == 0 {
		return errors.New("Cursor AvailableModels returned no usable models")
	}
	return persistCursorObservedModels(ctx, s.accountRepo, account.ID, ids, time.Now())
}

func cursorAvailableModelsURL(baseURL string, cfg *config.Config) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	var normalized string
	var err error
	if cfg == nil {
		if strings.TrimRight(baseURL, "/") != cursorpkg.DefaultBaseURL {
			return "", errors.New("invalid Cursor AvailableModels base URL: security config is unavailable for a custom host")
		}
		normalized = cursorpkg.DefaultBaseURL
	} else if !cfg.Security.URLAllowlist.Enabled {
		normalized, err = urlvalidator.ValidateURLFormat(baseURL, cfg.Security.URLAllowlist.AllowInsecureHTTP)
	} else {
		normalized, err = urlvalidator.ValidateHTTPSURL(baseURL, urlvalidator.ValidationOptions{
			AllowedHosts:     cfg.Security.URLAllowlist.UpstreamHosts,
			RequireAllowlist: true,
			AllowPrivate:     cfg.Security.URLAllowlist.AllowPrivateHosts,
		})
	}
	if err != nil {
		return "", fmt.Errorf("invalid Cursor AvailableModels base URL: %w", err)
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil ||
		(!strings.EqualFold(parsed.Scheme, "https") && !strings.EqualFold(parsed.Scheme, "http")) {
		return "", errors.New("invalid Cursor AvailableModels base URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/") + cursorpkg.EndpointAvailableModels
	return parsed.String(), nil
}

func cursorResolvedProxyURL(account *Account, now time.Time) (string, error) {
	if account == nil || account.ProxyID == nil {
		return "", nil
	}
	if account.Proxy == nil {
		return "", errors.New("Cursor account proxy is configured but unresolved")
	}
	if !account.Proxy.IsActive() {
		return "", errors.New("Cursor account proxy is disabled")
	}
	if account.Proxy.IsExpired(now) {
		return "", errors.New("Cursor account proxy is expired")
	}
	proxyURL := strings.TrimSpace(account.Proxy.URL())
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("Cursor account proxy is invalid")
	}
	return proxyURL, nil
}

func copyCursorHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func readCursorAvailableModelsBody(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, errors.New("Cursor AvailableModels returned an empty body")
	}
	body, err := io.ReadAll(io.LimitReader(reader, cursorAvailableModelsResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > cursorAvailableModelsResponseLimit {
		return nil, fmt.Errorf("Cursor AvailableModels response is too large (limit %d bytes)", cursorAvailableModelsResponseLimit)
	}
	return body, nil
}

func persistCursorObservedModels(ctx context.Context, repo AccountRepository, accountID int64, ids []string, fetchedAt time.Time) error {
	if repo == nil {
		return errors.New("account repository is not configured")
	}
	ids = normalizeCursorObservedModelIDs(ids)
	if len(ids) == 0 {
		return errors.New("Cursor AvailableModels returned no usable models")
	}
	snapshot := cursorObservedModelsSnapshot{
		Models: ids, FetchedAt: fetchedAt.UTC().Format(time.RFC3339), Source: "upstream_available_models",
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	return repo.UpdateExtra(ctx, accountID, map[string]any{cursorObservedModelsExtraKey: value})
}

func cursorObservedModelsFresh(extra map[string]any, now time.Time) bool {
	snapshot := parseCursorObservedModels(extra)
	if snapshot == nil {
		return false
	}
	fetchedAt, err := time.Parse(time.RFC3339, snapshot.FetchedAt)
	return err == nil && !fetchedAt.After(now) && now.Sub(fetchedAt) < cursorObservedModelsTTL
}

func cursorObservedModelIDs(models []cursorpkg.Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.Name)
	}
	return normalizeCursorObservedModelIDs(ids)
}

func normalizeCursorObservedModelIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func cursorObservedLookupKey(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	for _, suffix := range []string{"-max", ":max"} {
		if trimmed := strings.TrimSuffix(id, suffix); trimmed != id && trimmed != "" {
			id = trimmed
			break
		}
	}
	if id == "auto" || id == cursorpkg.AgentDefaultModel {
		return cursorpkg.AgentDefaultModel
	}
	return id
}

func CursorObservedModelSet(extra map[string]any) map[string]struct{} {
	ids := CursorObservedModelIDs(extra)
	if len(ids) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if key := cursorObservedLookupKey(id); key != "" {
			set[key] = struct{}{}
		}
	}
	return set
}

func CursorModelObserved(observed map[string]struct{}, target string) bool {
	if len(observed) == 0 {
		return false
	}
	_, ok := observed[cursorObservedLookupKey(target)]
	return ok
}

// CursorAvailableModelIDs returns the model IDs one account may advertise.
// A usable observed snapshot is authoritative; without one, the account's
// explicit mapping or the fork fallback catalogue is used.
func CursorAvailableModelIDs(account *Account) []string {
	if account == nil || account.Platform != PlatformCursor {
		return nil
	}
	if cursorObservedModelsAccountEligible(account) {
		if observedIDs := CursorObservedModelIDs(account.Extra); len(observedIDs) > 0 {
			ids := append([]string(nil), observedIDs...)
			if account.HasExplicitModelMapping() {
				observed := CursorObservedModelSet(account.Extra)
				for requested, target := range account.GetModelMapping() {
					if CursorModelObserved(observed, target) {
						ids = append(ids, requested)
					}
				}
			}
			return sortedCursorModelIDs(ids)
		}
	}

	ids := make([]string, 0)
	for requested := range account.GetModelMapping() {
		ids = append(ids, requested)
	}
	return sortedCursorModelIDs(ids)
}

func sortedCursorModelIDs(ids []string) []string {
	ids = normalizeCursorObservedModelIDs(ids)
	if len(ids) < 2 {
		return ids
	}
	sort.Strings(ids)
	return ids
}

func CursorObservedModelIDs(extra map[string]any) []string {
	snapshot := parseCursorObservedModels(extra)
	if snapshot == nil {
		return nil
	}
	return append([]string(nil), snapshot.Models...)
}

func parseCursorObservedModels(extra map[string]any) *cursorObservedModelsSnapshot {
	if extra == nil {
		return nil
	}
	raw, ok := extra[cursorObservedModelsExtraKey]
	if !ok || raw == nil {
		return nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snapshot cursorObservedModelsSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil
	}
	snapshot.Models = normalizeCursorObservedModelIDs(snapshot.Models)
	if len(snapshot.Models) == 0 {
		return nil
	}
	return &snapshot
}
