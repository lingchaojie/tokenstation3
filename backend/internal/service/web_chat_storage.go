package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type WebChatStorage interface {
	Save(ctx context.Context, in WebChatStorageSaveInput) (*WebChatStoredFile, error)
	Open(ctx context.Context, storageKey string) (io.ReadCloser, WebChatStoredFileMeta, error)
	Delete(ctx context.Context, storageKey string) error
}

type WebChatStorageSaveInput struct {
	UserID      int64
	Filename    string
	ContentType string
	Reader      io.Reader
	MaxBytes    int64
}

type WebChatStoredFile struct {
	StorageKey  string
	Filename    string
	ContentType string
	SizeBytes   int64
	SHA256      string
}

type WebChatStoredFileMeta struct {
	StorageKey string
	SizeBytes  int64
}

type LocalWebChatStorage struct {
	root string
}

func NewLocalWebChatStorage(root string) *LocalWebChatStorage {
	return &LocalWebChatStorage{root: root}
}

func NewLocalWebChatStorageFromConfig(cfg *config.Config) *LocalWebChatStorage {
	root := filepath.Join(".", "data")
	if cfg != nil {
		if configured := strings.TrimSpace(cfg.Pricing.DataDir); configured != "" {
			root = configured
		}
	}
	return NewLocalWebChatStorage(filepath.Join(root, "web-chat"))
}

func (s *LocalWebChatStorage) Save(ctx context.Context, in WebChatStorageSaveInput) (*WebChatStoredFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || strings.TrimSpace(s.root) == "" || in.Reader == nil || in.UserID <= 0 || in.MaxBytes <= 0 {
		return nil, ErrWebChatUploadRejected
	}

	filename := sanitizeWebChatDisplayFilename(in.Filename)
	ext := filepath.Ext(filename)
	if len(ext) > 32 {
		ext = ""
	}
	name, err := randomWebChatStorageName(ext)
	if err != nil {
		return nil, err
	}

	rel := filepath.Join(strconv.FormatInt(in.UserID, 10), time.Now().UTC().Format("2006/01/02"), name)
	fullPath := filepath.Join(s.root, rel)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(fullPath)
		}
	}()

	hasher := sha256.New()
	limited := io.LimitReader(in.Reader, in.MaxBytes+1)
	written, err := io.Copy(file, io.TeeReader(limited, hasher))
	if err != nil {
		return nil, err
	}
	if written > in.MaxBytes {
		return nil, ErrWebChatUploadRejected
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	keep = true

	return &WebChatStoredFile{
		StorageKey:  filepath.ToSlash(rel),
		Filename:    filename,
		ContentType: in.ContentType,
		SizeBytes:   written,
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func (s *LocalWebChatStorage) Open(ctx context.Context, storageKey string) (io.ReadCloser, WebChatStoredFileMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, WebChatStoredFileMeta{}, err
	}
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil, WebChatStoredFileMeta{}, ErrWebChatUploadRejected
	}
	rel, err := cleanWebChatStorageKey(storageKey)
	if err != nil {
		return nil, WebChatStoredFileMeta{}, err
	}
	fullPath, err := resolveWebChatStoragePath(s.root, rel)
	if err != nil {
		return nil, WebChatStoredFileMeta{}, err
	}
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, WebChatStoredFileMeta{}, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, WebChatStoredFileMeta{}, err
	}
	return file, WebChatStoredFileMeta{
		StorageKey: filepath.ToSlash(rel),
		SizeBytes:  info.Size(),
	}, nil
}

func (s *LocalWebChatStorage) Delete(ctx context.Context, storageKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || strings.TrimSpace(s.root) == "" {
		return ErrWebChatUploadRejected
	}
	rel, err := cleanWebChatStorageKey(storageKey)
	if err != nil {
		return err
	}
	fullPath, err := resolveWebChatStoragePath(s.root, rel)
	if err != nil {
		return err
	}
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func randomWebChatStorageName(ext string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate web chat storage name: %w", err)
	}
	return hex.EncodeToString(raw[:]) + strings.ToLower(ext), nil
}

func cleanWebChatStorageKey(storageKey string) (string, error) {
	key := filepath.Clean(filepath.FromSlash(strings.TrimSpace(storageKey)))
	if key == "." || key == "" || key == ".." || filepath.IsAbs(key) || strings.HasPrefix(key, ".."+string(filepath.Separator)) {
		return "", ErrWebChatUploadRejected
	}
	return key, nil
}

func resolveWebChatStoragePath(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fullPath := filepath.Join(rootAbs, rel)
	fullAbs, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}
	inside, err := filepath.Rel(rootAbs, fullAbs)
	if err != nil {
		return "", err
	}
	if inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", ErrWebChatUploadRejected
	}
	return fullAbs, nil
}

func sanitizeWebChatDisplayFilename(raw string) string {
	name := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	name = filepath.Base(name)
	if name == "." || name == ".." || name == "" || name == string(filepath.Separator) {
		name = "upload"
	}
	var b strings.Builder
	b.Grow(len(name))
	lastUnderscore := false
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
			continue
		}
		b.WriteRune(r)
		lastUnderscore = false
	}
	name = strings.TrimSpace(b.String())
	name = strings.Trim(name, ".")
	if name == "" {
		name = "upload"
	}
	return truncateWebChatFilename(name, 255)
}

func truncateWebChatFilename(name string, maxBytes int) string {
	if len(name) <= maxBytes {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) >= maxBytes {
		ext = ""
	}
	baseLimit := maxBytes - len(ext)
	base := strings.TrimSuffix(name, ext)
	if baseLimit <= 0 {
		baseLimit = maxBytes
		ext = ""
	}
	baseBytes := []byte(base)
	if len(baseBytes) > baseLimit {
		baseBytes = baseBytes[:baseLimit]
	}
	for len(baseBytes) > 0 && !utf8.Valid(baseBytes) {
		baseBytes = baseBytes[:len(baseBytes)-1]
	}
	base = strings.Trim(string(baseBytes), ". ")
	if base == "" {
		base = "upload"
	}
	out := base + ext
	if len(out) <= maxBytes {
		return out
	}
	outBytes := []byte(out)
	outBytes = outBytes[:maxBytes]
	for len(outBytes) > 0 && !utf8.Valid(outBytes) {
		outBytes = outBytes[:len(outBytes)-1]
	}
	return string(outBytes)
}
