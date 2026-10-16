// Package publish validates publish inits before they reach TikTok.
//
// @file      internal/publish/service.go
// @for       Video init validation orchestration and shared service types.
// @uses      context, encoding/json, internal/apierror, internal/tiktokclient
// @reason    Centralizes the 003 section 7.4 rules so handlers stay thin.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// Length caps are counted in UTF-16 code units, the unit TikTok enforces.
const (
	maxVideoTitleLen  = 2200
	maxPhotoTitleLen  = 90
	maxPhotoDescLen   = 4000
	defaultChunkSize  = 10 << 20
	privacySelfOnly   = "SELF_ONLY"
	sourcePullFromURL = "PULL_FROM_URL"
	sourceFileUpload  = "FILE_UPLOAD"
	modeDirectPost    = "DIRECT_POST"
)

// Poster is the TikTok client slice this service needs. The narrow shape lets
// *tiktokclient.Client satisfy it with no wrapper.
type Poster interface {
	QueryCreatorInfo(ctx context.Context, access string) (tiktokclient.CreatorInfo, error)
	InitVideoPost(ctx context.Context, access string, req tiktokclient.VideoInitRequest) (tiktokclient.VideoInitResponse, error)
	InitPhotoPost(ctx context.Context, access string, req tiktokclient.PhotoInitRequest) (tiktokclient.PhotoInitResponse, error)
	InitDraftUpload(ctx context.Context, access string, req tiktokclient.DraftInitRequest) (tiktokclient.DraftInitResponse, error)
	FetchStatus(ctx context.Context, access string, publishID string) (tiktokclient.PublishStatus, error)
}

// AccountResolver returns the access token for an account the operator owns.
type AccountResolver interface {
	Owned(ctx context.Context, operatorID string, accountID string) (access string, err error)
}

// CreatorOptions returns the current privacy_level_options for the account.
// The operator id travels with the lookup so the provider can enforce
// ownership before it reads or refetches anything.
type CreatorOptions interface {
	Options(ctx context.Context, operatorID string, accountID string) ([]string, error)
}

// AttemptWriter persists one attempt row. *Repository satisfies it directly.
type AttemptWriter interface {
	Insert(ctx context.Context, rec Record) (Record, error)
}

// IdempotencyClaimer claims one outcome per key and body hash.
// *idempotency.Store satisfies it directly.
type IdempotencyClaimer interface {
	Begin(ctx context.Context, key, bodyHash string) ([]byte, error)
	Complete(ctx context.Context, key, bodyHash string, response []byte) error
	Release(ctx context.Context, key, bodyHash string) error
}

var (
	_ Poster        = (*tiktokclient.Client)(nil)
	_ AttemptWriter = (*Repository)(nil)
)

// UploadPlan tells the caller how to deliver the bytes. It never carries the
// upstream upload URL, which stays server side.
type UploadPlan struct {
	Source      string `json:"source"`
	ChunkSize   int64  `json:"chunk_size,omitempty"`
	TotalChunks int    `json:"total_chunks,omitempty"`
}

// InitResult is the stored 202 shape for one init: the attempt row identity,
// the TikTok publish id, the attempt status, and the upload plan.
type InitResult struct {
	ID         string     `json:"id"`
	PublishID  string     `json:"publish_id"`
	Status     Status     `json:"status"`
	UploadPlan UploadPlan `json:"upload_plan"`
}

// Service enforces the init validation rules in 003 section 7.4 order. It
// never writes tokens, so no credential writes happen here.
type Service struct {
	poster      Poster
	accounts    AccountResolver
	creators    CreatorOptions
	attempts    AttemptWriter
	idempotency IdempotencyClaimer
	mediaHost   string
	validate    *validator.Validate
	chunks      chunkDeps
}

// chunkDeps is the collaborator set the PutChunk flow in chunks.go needs.
type chunkDeps struct {
	attempts AttemptStore
	store    ChunkStore
	plans    *MemoryPlanStore
	uploader ChunkPutter
	ttl      time.Duration
	now      func() time.Time
}

// ChunkServiceConfig wires the chunk collaborators onto an existing Service.
type ChunkServiceConfig struct {
	Attempts AttemptStore
	Chunks   ChunkStore
	Plans    *MemoryPlanStore
	Uploader ChunkPutter
	TTL      time.Duration
	Now      func() time.Time
}

// WithChunkProxy extends the Service with the chunk collaborators, so one
// Service serves both the init flows and the chunk upload proxy.
func (s *Service) WithChunkProxy(cfg ChunkServiceConfig) *Service {
	if cfg.Plans == nil {
		cfg.Plans = NewMemoryPlanStore()
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaultUploadURLTTL
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	s.chunks = chunkDeps{
		attempts: cfg.Attempts, store: cfg.Chunks, plans: cfg.Plans,
		uploader: cfg.Uploader, ttl: cfg.TTL, now: cfg.Now,
	}
	return s
}

// Config wires one Service. MediaHost is the verified PULL_FROM_URL host.
type Config struct {
	Poster      Poster
	Accounts    AccountResolver
	Creators    CreatorOptions
	Attempts    AttemptWriter
	Idempotency IdempotencyClaimer
	MediaHost   string
}

// NewService wires validation and its collaborators together.
func NewService(cfg Config) *Service {
	return &Service{
		poster: cfg.Poster, accounts: cfg.Accounts, creators: cfg.Creators,
		attempts: cfg.Attempts, idempotency: cfg.Idempotency,
		mediaHost: strings.ToLower(cfg.MediaHost), validate: validator.New(),
	}
}

// ChunkPutter forwards one validated chunk upstream to the upload URL.
type ChunkPutter interface {
	UploadChunk(ctx context.Context, uploadURL string, chunk tiktokclient.Chunk) error
}

// ChunkStore is the repository slice the chunk flow reads and writes.
type ChunkStore interface {
	RecordChunk(ctx context.Context, rec ChunkRecord) error
	Chunks(ctx context.Context, attemptID string) ([]ChunkRecord, error)
	UpdateProgress(ctx context.Context, attemptID string, uploadedBytes int64) error
}

// AttemptStore reads the attempt row the chunk flow validates against and
// persists a restarted upload plan.
type AttemptStore interface {
	Get(ctx context.Context, id string) (Record, error)
	GetByPublishID(ctx context.Context, publishID string) (Record, error)
	Update(ctx context.Context, rec Record) error
}

var (
	_ ChunkPutter  = (*tiktokclient.Client)(nil)
	_ ChunkStore   = (*Repository)(nil)
	_ AttemptStore = (*Repository)(nil)
)

// UploadWindow is the plan one upload URL was issued for: chunk size, chunk
// count, and total bytes. The upload URL itself never leaves the server.
type UploadWindow struct {
	PublishID   string
	UploadURL   string
	ChunkSize   int64
	TotalChunks int
	TotalBytes  int64
	ContentType string
	FirstChunk  int
	InitAt      time.Time
}

// Expired reports whether the upload URL is past its TTL at now.
func (w UploadWindow) Expired(now time.Time, ttl time.Duration) bool {
	return now.After(w.InitAt.Add(ttl))
}

// MemoryPlanStore keeps the live upload windows by attempt id. A reconciler
// rebuilds a window from the attempt row, so no durable table holds the URLs.
type MemoryPlanStore struct {
	mu      sync.RWMutex
	windows map[string]UploadWindow
}

// NewMemoryPlanStore returns an empty plan store.
func NewMemoryPlanStore() *MemoryPlanStore {
	return &MemoryPlanStore{windows: make(map[string]UploadWindow)}
}

// Get returns the window stored for an attempt.
func (m *MemoryPlanStore) Get(_ context.Context, attemptID string) (UploadWindow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	window, ok := m.windows[attemptID]
	return window, ok
}

// Save stores or replaces the window of an attempt.
func (m *MemoryPlanStore) Save(_ context.Context, attemptID string, window UploadWindow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.windows[attemptID] = window
	return nil
}
