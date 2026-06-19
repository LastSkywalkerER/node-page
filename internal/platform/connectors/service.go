package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	hosts "system-stats/internal/cluster/hosts"
)

// Prober validates connector credentials and fingerprints the remote side.
// Implemented per connector type (internal/metrics/proxmox for TypeProxmox)
// and injected via DI so this package stays free of API-client dependencies.
type Prober interface {
	Probe(ctx context.Context, endpoint, tokenID, secret string, skipTLSVerify bool) (*ProbeResult, error)
}

// PexelsProber validates a Pexels API key (internal/platform/wallpaper).
type PexelsProber interface {
	ProbeKey(ctx context.Context, apiKey string) error
}

// RaftReplicator is the subset of the raft replicator the connectors service
// needs. Defined locally to avoid an import cycle (raft imports this package
// for the repository interface used by its appliers).
type RaftReplicator interface {
	Enabled() bool
	SubmitConnectorUpsert(ctx context.Context, c Connector) error
	SubmitConnectorDelete(ctx context.Context, connectorType, fingerprint string, removeHosts bool) error
}

// ConnectRequest is the create/test payload for a Proxmox connector.
type ConnectRequest struct {
	Endpoint      string `json:"endpoint" binding:"required"`
	TokenID       string `json:"token_id" binding:"required"`
	Secret        string `json:"secret" binding:"required"`
	SkipTLSVerify bool   `json:"skip_tls_verify"`
}

// ListResult is the GET /connectors response body.
type ListResult struct {
	// Discovered holds environment hints without a matching configured
	// connector ("we look like a Proxmox guest — connect it?").
	Discovered []DiscoveredHint `json:"discovered"`
	Configured []Connector      `json:"configured"`
}

// ErrAuthFailed marks a credentials problem (401/403 from the remote API) so
// the handler can answer 400 instead of 502.
var ErrAuthFailed = errors.New("connector authentication failed")

// PexelsRequest creates/updates the dynamic-wallpaper connector. An empty
// APIKey on update keeps the stored secret (lets the admin change only the
// query without re-pasting the key).
type PexelsRequest struct {
	APIKey string `json:"api_key"`
	Query  string `json:"query"`
}

// Service is the admin-facing connector registry.
type Service interface {
	List(ctx context.Context) (*ListResult, error)
	TestProxmox(ctx context.Context, req ConnectRequest) (*Preview, error)
	CreateProxmox(ctx context.Context, req ConnectRequest) (*Connector, error)
	TestPBS(ctx context.Context, req ConnectRequest) (*Preview, error)
	CreatePBS(ctx context.Context, req ConnectRequest) (*Connector, error)
	SavePexels(ctx context.Context, req PexelsRequest) (*Connector, error)
	SetEnabled(ctx context.Context, id uint, enabled bool) (*Connector, error)
	Delete(ctx context.Context, id uint, removeHosts bool) error
	// TriggerSync pokes the poller for an immediate resync (best-effort).
	TriggerSync()
	// SetSyncTrigger registers the poller's wake-up callback.
	SetSyncTrigger(fn func())
	// BootstrapProxmoxFromEnv auto-creates a Proxmox connector from the
	// NODE_STATS_PROXMOX_* env vars on first boot (idempotent, best-effort).
	// Used by the Proxmox-LXC installer so the node self-attaches to its host.
	BootstrapProxmoxFromEnv(ctx context.Context)
}

type service struct {
	logger        *log.Logger
	repo          Repository
	hostRepo      hosts.Repository
	detector      *Detector
	cipher        *Cipher
	proxmoxProber Prober
	pbsProber     Prober
	pexelsProber  PexelsProber
	raft          RaftReplicator

	mu          sync.Mutex
	syncTrigger func()
}

// NewService wires the connector registry.
func NewService(logger *log.Logger, repo Repository, hostRepo hosts.Repository, detector *Detector, cipher *Cipher, proxmoxProber, pbsProber Prober, pexelsProber PexelsProber, raft RaftReplicator) Service {
	return &service{
		logger:        logger,
		repo:          repo,
		hostRepo:      hostRepo,
		detector:      detector,
		cipher:        cipher,
		proxmoxProber: proxmoxProber,
		pbsProber:     pbsProber,
		pexelsProber:  pexelsProber,
		raft:          raft,
	}
}

func (s *service) List(ctx context.Context) (*ListResult, error) {
	configured, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	configuredTypes := make(map[string]bool, len(configured))
	for _, c := range configured {
		configuredTypes[c.Type] = true
	}
	// A configured connector of the same type satisfies the hint; we cannot
	// compare fingerprints credential-free, and one Proxmox connector per
	// install is the common case.
	discovered := make([]DiscoveredHint, 0)
	for _, h := range s.detector.Detect() {
		if !configuredTypes[h.Type] {
			discovered = append(discovered, h)
		}
	}
	return &ListResult{Discovered: discovered, Configured: configured}, nil
}

func (s *service) probe(ctx context.Context, req ConnectRequest) (*ProbeResult, error) {
	res, err := s.proxmoxProber.Probe(ctx,
		strings.TrimRight(strings.TrimSpace(req.Endpoint), "/"),
		strings.TrimSpace(req.TokenID),
		req.Secret,
		req.SkipTLSVerify,
	)
	if err != nil {
		return nil, err
	}
	if res.Fingerprint == "" {
		return nil, fmt.Errorf("proxmox probe returned no fingerprint")
	}
	return res, nil
}

func (s *service) TestProxmox(ctx context.Context, req ConnectRequest) (*Preview, error) {
	res, err := s.probe(ctx, req)
	if err != nil {
		return nil, err
	}
	// Count distinct already-registered hosts reachable by either linking key
	// (NIC MAC or SMBIOS UUID) — the same set the poller will link, not dupe.
	matchedIDs := map[uint]bool{}
	for _, mac := range res.GuestMACs {
		if mac == "" {
			continue
		}
		if h, err := s.hostRepo.GetHostByMacAddress(ctx, strings.ToLower(mac)); err == nil {
			matchedIDs[h.ID] = true
		}
	}
	for _, uuid := range res.GuestUUIDs {
		if h, err := s.hostRepo.GetHostByHardwareUUID(ctx, uuid); err == nil {
			matchedIDs[h.ID] = true
		}
	}
	return &Preview{ProbeResult: *res, MatchedHosts: len(matchedIDs)}, nil
}

func (s *service) CreateProxmox(ctx context.Context, req ConnectRequest) (*Connector, error) {
	res, err := s.probe(ctx, req)
	if err != nil {
		return nil, err
	}
	secretEnc, err := s.cipher.Encrypt(req.Secret)
	if err != nil {
		return nil, err
	}
	conn := Connector{
		Type:          TypeProxmox,
		Endpoint:      strings.TrimRight(strings.TrimSpace(req.Endpoint), "/"),
		TokenID:       strings.TrimSpace(req.TokenID),
		SecretEnc:     secretEnc,
		SkipTLSVerify: req.SkipTLSVerify,
		Fingerprint:   res.Fingerprint,
		Enabled:       true,
	}
	if err := s.persistUpsert(ctx, conn); err != nil {
		return nil, err
	}
	s.TriggerSync()
	return s.repo.GetByFingerprint(ctx, conn.Fingerprint)
}

func (s *service) TestPBS(ctx context.Context, req ConnectRequest) (*Preview, error) {
	res, err := s.probePBS(ctx, req)
	if err != nil {
		return nil, err
	}
	// PBS has no guests to match against registered hosts; the preview just
	// reports reachability + datastore count (carried in GuestCount).
	return &Preview{ProbeResult: *res}, nil
}

func (s *service) CreatePBS(ctx context.Context, req ConnectRequest) (*Connector, error) {
	res, err := s.probePBS(ctx, req)
	if err != nil {
		return nil, err
	}
	secretEnc, err := s.cipher.Encrypt(req.Secret)
	if err != nil {
		return nil, err
	}
	conn := Connector{
		Type:          TypePBS,
		Endpoint:      strings.TrimRight(strings.TrimSpace(req.Endpoint), "/"),
		TokenID:       strings.TrimSpace(req.TokenID),
		SecretEnc:     secretEnc,
		SkipTLSVerify: req.SkipTLSVerify,
		Fingerprint:   res.Fingerprint,
		Enabled:       true,
	}
	if err := s.persistUpsert(ctx, conn); err != nil {
		return nil, err
	}
	s.TriggerSync()
	return s.repo.GetByFingerprint(ctx, conn.Fingerprint)
}

func (s *service) probePBS(ctx context.Context, req ConnectRequest) (*ProbeResult, error) {
	res, err := s.pbsProber.Probe(ctx,
		strings.TrimRight(strings.TrimSpace(req.Endpoint), "/"),
		strings.TrimSpace(req.TokenID),
		req.Secret,
		req.SkipTLSVerify,
	)
	if err != nil {
		return nil, err
	}
	if res.Fingerprint == "" {
		return nil, fmt.Errorf("pbs probe returned no fingerprint")
	}
	return res, nil
}

func (s *service) SavePexels(ctx context.Context, req PexelsRequest) (*Connector, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		query = "abstract dark technology"
	}
	apiKey := strings.TrimSpace(req.APIKey)

	var secretEnc []byte
	if apiKey != "" {
		if err := s.pexelsProber.ProbeKey(ctx, apiKey); err != nil {
			return nil, err
		}
		enc, err := s.cipher.Encrypt(apiKey)
		if err != nil {
			return nil, err
		}
		secretEnc = enc
	} else {
		// Query-only update: keep the stored key.
		existing, err := s.repo.GetByFingerprint(ctx, PexelsFingerprint)
		if err != nil {
			return nil, fmt.Errorf("an API key is required for the first setup")
		}
		secretEnc = existing.SecretEnc
	}

	cfg, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, err
	}
	conn := Connector{
		Type:        TypePexels,
		Endpoint:    "https://api.pexels.com",
		SecretEnc:   secretEnc,
		Fingerprint: PexelsFingerprint,
		Config:      string(cfg),
		Enabled:     true,
	}
	if err := s.persistUpsert(ctx, conn); err != nil {
		return nil, err
	}
	return s.repo.GetByFingerprint(ctx, PexelsFingerprint)
}

func (s *service) SetEnabled(ctx context.Context, id uint, enabled bool) (*Connector, error) {
	conn, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	conn.Enabled = enabled
	if err := s.persistUpsert(ctx, *conn); err != nil {
		return nil, err
	}
	if enabled {
		s.TriggerSync()
	}
	return s.repo.GetByID(ctx, id)
}

// persistUpsert routes a connector mutation through Raft when enabled (the
// applier writes on every node, including this one) or straight to the local
// repository when standalone.
func (s *service) persistUpsert(ctx context.Context, conn Connector) error {
	if s.raft != nil && s.raft.Enabled() {
		return s.raft.SubmitConnectorUpsert(ctx, conn)
	}
	return s.repo.Upsert(ctx, &conn)
}

func (s *service) Delete(ctx context.Context, id uint, removeHosts bool) error {
	conn, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if s.raft != nil && s.raft.Enabled() {
		return s.raft.SubmitConnectorDelete(ctx, conn.Type, conn.Fingerprint, removeHosts)
	}
	if err := s.repo.DeleteByFingerprint(ctx, conn.Fingerprint); err != nil {
		return err
	}
	return s.hostRepo.UnlinkConnectorHosts(ctx, ExternalIDPrefix(conn.Type, conn.Fingerprint), removeHosts)
}

func (s *service) TriggerSync() {
	s.mu.Lock()
	fn := s.syncTrigger
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *service) SetSyncTrigger(fn func()) {
	s.mu.Lock()
	s.syncTrigger = fn
	s.mu.Unlock()
}
