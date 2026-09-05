package appbackup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/app/dockerenv"
	docker "system-stats/internal/metrics/docker"
	"system-stats/internal/platform/connectors"
	"system-stats/internal/platform/setup"
)

// connectorTypeAppBackup is this feature's connector type in the shared
// registry; it stores a repository, not a data source, so no host rows hang
// off it.
const connectorTypeAppBackup = "appbackup"

// Errors the handler maps to HTTP status codes.
var (
	ErrNoRepo       = errors.New("no backup repository configured")
	ErrNoController = errors.New("this deployment has no controller sidecar, so it cannot stop containers or write files on the host")
	ErrRemoteHost   = errors.New("this application runs on another node; open that node's dashboard to act on it")
	ErrSelfProject  = errors.New("node-stats cannot back up its own stack: the job would have to stop the containers running it")
	ErrBusy         = errors.New("another job is already running for this application")
	// ErrRepoReadOnly is returned when a filesystem repository is not writable
	// by this process. restic needs read-write access to manage snapshots at
	// all — and given a read-only repository it does not fail, it blocks
	// forever waiting for a lock it can never take — so the refusal is ours and
	// it happens when the repository is configured, not when a delete is tried.
	ErrRepoReadOnly = errors.New("the repository must be readable AND writable by node-stats")
)

// ConnectorStore is the subset of the connector registry this package needs.
// Reusing that registry gives the repository row Raft replication, AES-GCM
// secret storage and the admin CRUD shape for free.
type ConnectorStore interface {
	GetByFingerprint(ctx context.Context, fingerprint string) (*connectors.Connector, error)
	Upsert(ctx context.Context, c *connectors.Connector) error
	DeleteByFingerprint(ctx context.Context, fingerprint string) error
}

// Replicator mirrors the connector row to the rest of the cluster, so every
// node backs up to the same repository.
type Replicator interface {
	Enabled() bool
	SubmitConnectorUpsert(ctx context.Context, c connectors.Connector) error
	SubmitConnectorDelete(ctx context.Context, connectorType, fingerprint string, removeHosts bool) error
}

// Status tells the UI what it may offer and, when it may not, exactly why.
// Actions are never hidden — an operator who cannot back up wants to know the
// reason, not to wonder where the button went.
type Status struct {
	RepoConfigured  bool   `json:"repo_configured"`
	Repo            *View  `json:"repo,omitempty"`
	ControllerReady bool   `json:"controller_ready"`
	ResticInstalled bool   `json:"restic_installed"`
	ResticVersion   string `json:"restic_version,omitempty"`
	// Scope is "node" for a filesystem repository (a directory only this
	// machine can reach) and "cluster" for SSH/S3 (shared by every node).
	Scope string `json:"scope,omitempty"`
	// SuggestedPath prefills the filesystem form with a directory beside the
	// installation, so the operator names a place they recognise.
	SuggestedPath string `json:"suggested_path,omitempty"`
	// MountPending is true while a filesystem repository is configured but the
	// bind mount has not taken effect yet — the controller is recreating the
	// app, and backups cannot run until it comes back.
	MountPending bool `json:"mount_pending,omitempty"`
	// SelfMountRequired marks a deployment where node-stats cannot add the
	// mount itself (managed externally); MountHint says what to mount.
	SelfMountRequired bool   `json:"self_mount_required,omitempty"`
	MountHint         string `json:"mount_hint,omitempty"`
	// Reason is a human sentence explaining the first blocker, empty when ready.
	Reason string `json:"reason,omitempty"`
}

// View is the repository as shown in the admin UI — never the secrets.
type View struct {
	Backend string `json:"backend"`
	URL     string `json:"url"`
	Config  RepoConfig
}

// MarshalJSON flattens the config into the view so the form binds directly.
func (v View) MarshalJSON() ([]byte, error) {
	m := map[string]any{"backend": v.Backend, "url": v.URL}
	b, err := json.Marshal(v.Config)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	for k, val := range cfg {
		m[k] = val
	}
	return json.Marshal(m)
}

// RepoRequest is the admin's save/test payload.
type RepoRequest struct {
	RepoConfig
	Password      string `json:"password"`
	S3SecretKey   string `json:"s3_secret_key,omitempty"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
}

// Plan is what the UI needs to offer backup and update for one application.
type Plan struct {
	Project      string         `json:"project"`
	HostID       uint           `json:"host_id"`
	Local        bool           `json:"local"`
	ComposeFiles []string       `json:"compose_files"`
	ProjectDir   string         `json:"project_dir"`
	Paths        []BackupPath   `json:"paths"`
	Services     []ServiceState `json:"services"`
	TotalSize    int64          `json:"total_size"`
	// Blocked explains why actions are unavailable, empty when they are not.
	Blocked string `json:"blocked,omitempty"`
}

// Service is the application-backup API.
type Service interface {
	Status(ctx context.Context) (*Status, error)
	SaveRepo(ctx context.Context, req RepoRequest) (*Status, error)
	TestRepo(ctx context.Context, req RepoRequest) error
	DeleteRepo(ctx context.Context) error

	Plan(ctx context.Context, hostID uint, project string) (*Plan, error)
	Versions(ctx context.Context, image string) ([]string, error)
	Snapshots(ctx context.Context, project string) ([]Snapshot, error)
	RepoStats(ctx context.Context) (*RepoStats, error)
	DeleteSnapshot(ctx context.Context, id string) error

	Backup(ctx context.Context, hostID uint, project string) (*RunEntity, error)
	Update(ctx context.Context, hostID uint, project string, targets []ServiceTarget) (*RunEntity, error)
	Restore(ctx context.Context, hostID uint, project, snapshotID string) (*RunEntity, error)

	Runs(ctx context.Context, hostID uint, project string, limit int) ([]RunEntity, error)
	// SyncRuns folds the controller's status file into the run history. Called
	// on a timer and before serving the history, so the UI sees progress
	// without the controller needing database access.
	SyncRuns(ctx context.Context) error
}

type service struct {
	logger     *log.Logger
	repo       Repository
	dockerSvc  docker.Service
	connectors ConnectorStore
	localRepo  LocalRepoStore
	cipher     *connectors.Cipher
	raft       Replicator
	dataDir    string
	// dbType/dbDSN seed a desired-state file that does not exist yet when the
	// backup mount is requested.
	dbType, dbDSN string
	// localHostID is the host row this process collects for (hosts.LocalCollectorHostID).
	localHostID uint

	mu sync.Mutex
}

// NewService wires the application-backup service.
func NewService(logger *log.Logger, repo Repository, dockerSvc docker.Service, store ConnectorStore, localRepo LocalRepoStore, cipher *connectors.Cipher, raft Replicator, dataDir, dbType, dbDSN string, localHostID uint) Service {
	return &service{
		logger: logger, repo: repo, dockerSvc: dockerSvc,
		connectors: store, localRepo: localRepo, cipher: cipher, raft: raft,
		dataDir: dataDir, dbType: dbType, dbDSN: dbDSN, localHostID: localHostID,
	}
}

// --- repository configuration -------------------------------------------------

func (s *service) Status(ctx context.Context) (*Status, error) {
	st := &Status{
		ControllerReady: s.controllerReady(),
		ResticVersion:   InstalledVersion(s.dataDir),
	}
	st.ResticInstalled = st.ResticVersion != ""

	st.SuggestedPath = SuggestedBackupPath(s.dataDir, dockerenv.Running())
	st.SelfMountRequired = dockerenv.Running() && setup.ManagedExternally()
	if st.SelfMountRequired {
		st.MountHint = "this deployment is managed by an external orchestrator, so node-stats cannot add the mount itself — bind-mount the directory into the node-stats container at " + setup.BackupMountPath
	}

	cfg, _, err := s.loadRepo(ctx)
	if err == nil && cfg != nil {
		st.RepoConfigured = true
		st.Repo = &View{Backend: cfg.Backend, URL: RepoURL(*cfg), Config: *cfg}
		st.Scope = "cluster"
		if cfg.Backend == BackendLocal {
			st.Scope = "node"
			// The mount only exists after the controller has recreated the app.
			st.MountPending = !dirExists(s.appRepoPath(*cfg))
		}
	}

	switch {
	case !st.RepoConfigured:
		st.Reason = "No backup repository configured yet."
	case st.MountPending && st.SelfMountRequired:
		st.Reason = st.MountHint
	case st.MountPending:
		st.Reason = "node-stats is being recreated with the backup directory mounted; backups can run once it is back."
	case !st.ControllerReady:
		st.Reason = ErrNoController.Error()
	case !st.ResticInstalled:
		st.Reason = "restic is not installed yet; saving the repository installs it."
	}
	return st, nil
}

// controllerReady reports whether a controller sidecar is running here. Its
// heartbeat file is the only signal the app has — it holds the docker socket,
// the app deliberately does not.
func (s *service) controllerReady() bool {
	fi, err := os.Stat(filepath.Join(s.dataDir, "controller-status.json"))
	if err != nil {
		return false
	}
	// A stale heartbeat means the sidecar died; the controller rewrites it on
	// every state change and at least whenever a job runs.
	return time.Since(fi.ModTime()) < 24*time.Hour
}

// loadRepo returns this node's effective repository. A filesystem repository is
// node-local and wins over the replicated one: it is this machine's explicit
// choice, and a directory here is not something another node could have meant.
func (s *service) loadRepo(ctx context.Context) (*RepoConfig, *RepoSecrets, error) {
	if local, err := s.localRepo.Get(ctx); err == nil && local != nil {
		return decodeRepo(local.Config, local.SecretEnc, s.cipher)
	}
	c, err := s.connectors.GetByFingerprint(ctx, ConnectorFingerprint)
	if err != nil || c == nil {
		return nil, nil, ErrNoRepo
	}
	return decodeRepo(c.Config, c.SecretEnc, s.cipher)
}

func decodeRepo(cfgJSON string, enc []byte, cipher *connectors.Cipher) (*RepoConfig, *RepoSecrets, error) {
	var cfg RepoConfig
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return nil, nil, fmt.Errorf("repository config unreadable: %w", err)
	}
	plain, err := cipher.Decrypt(enc)
	if err != nil {
		return nil, nil, fmt.Errorf("repository secrets unreadable: %w", err)
	}
	var sec RepoSecrets
	if err := json.Unmarshal([]byte(plain), &sec); err != nil {
		return nil, nil, fmt.Errorf("repository secrets unreadable: %w", err)
	}
	return &cfg, &sec, nil
}

func (s *service) SaveRepo(ctx context.Context, req RepoRequest) (*Status, error) {
	if err := validateRepo(req); err != nil {
		return nil, err
	}
	if req.Backend == BackendLocal {
		return s.saveLocalRepo(ctx, req)
	}
	// Install restic before persisting: a repository we cannot reach is worse
	// than no repository, and the operator asked for restic to arrive with the
	// configuration rather than in the image.
	if _, err := EnsureBinary(ctx, s.dataDir); err != nil {
		return nil, fmt.Errorf("install restic: %w", err)
	}
	if err := s.TestRepo(ctx, req); err != nil {
		return nil, err
	}

	sec := RepoSecrets{Password: req.Password, S3SecretKey: req.S3SecretKey, SSHPrivateKey: req.SSHPrivateKey}
	secJSON, err := json.Marshal(sec)
	if err != nil {
		return nil, err
	}
	enc, err := s.cipher.Encrypt(string(secJSON))
	if err != nil {
		return nil, err
	}
	cfgJSON, err := json.Marshal(req.RepoConfig)
	if err != nil {
		return nil, err
	}

	conn := connectors.Connector{
		Type:        connectorTypeAppBackup,
		Fingerprint: ConnectorFingerprint,
		Endpoint:    RepoURL(req.RepoConfig),
		Config:      string(cfgJSON),
		SecretEnc:   enc,
		Enabled:     true,
		Status:      connectors.StatusOK,
	}
	if existing, _ := s.connectors.GetByFingerprint(ctx, ConnectorFingerprint); existing != nil {
		conn.ID = existing.ID
	}
	// With Raft on, the log IS the write path — it applies locally too, so
	// writing directly as well would race the applied entry.
	if s.raft != nil && s.raft.Enabled() {
		if err := s.raft.SubmitConnectorUpsert(ctx, conn); err != nil {
			return nil, err
		}
	} else if err := s.connectors.Upsert(ctx, &conn); err != nil {
		return nil, err
	}
	return s.Status(ctx)
}

// saveLocalRepo stores a filesystem repository and arranges for this process to
// be able to reach it.
//
// The operator names a path on the MACHINE. Inside a container that path is not
// visible, so node-stats asks the controller to bind-mount it at a fixed
// in-container location and the app is recreated — the same mechanism that
// already applies a database or gateway change. Until it comes back the
// repository reads as "mount pending"; on a native install there is nothing to
// mount and it is usable immediately.
//
// The row is node-local: a directory on this machine is unreachable from any
// other node, so replicating it would leave peers pointing at a path that does
// not exist there.
func (s *service) saveLocalRepo(ctx context.Context, req RepoRequest) (*Status, error) {
	if _, err := EnsureBinary(ctx, s.dataDir); err != nil {
		return nil, fmt.Errorf("install restic: %w", err)
	}
	if dockerenv.Running() && setup.ManagedExternally() {
		// We cannot add the mount here; the repository is only usable if the
		// operator already mounted it, so require that up front rather than
		// storing a configuration that silently cannot work.
		if err := assertRepoWritable(setup.BackupMountPath); err != nil {
			return nil, fmt.Errorf("%w — bind-mount %s into the node-stats container at %s", ErrRepoReadOnly, req.Path, setup.BackupMountPath)
		}
	}

	sec := RepoSecrets{Password: req.Password}
	row, err := marshalLocalRepo(req.RepoConfig, sec, s.cipher)
	if err != nil {
		return nil, err
	}
	if err := s.localRepo.Save(ctx, row); err != nil {
		return nil, err
	}
	if _, err := setup.RequestBackupMount(s.dataDir, s.dbType, s.dbDSN, req.Path); err != nil {
		return nil, err
	}
	s.logger.Info("appbackup: filesystem repository configured", "path", req.Path)
	return s.Status(ctx)
}

// TestRepo verifies the credentials by asking restic to open (or create) the
// repository, which is the only check that proves the whole path works.
//
// Every repository must be read-write: restic cannot manage snapshots — prune,
// forget, even lock — without it, so a read-only one is refused here rather
// than half-working until the first delete.
func (s *service) TestRepo(ctx context.Context, req RepoRequest) error {
	if err := validateRepo(req); err != nil {
		return err
	}
	bin, err := EnsureBinary(ctx, s.dataDir)
	if err != nil {
		return fmt.Errorf("install restic: %w", err)
	}
	if req.Backend == BackendLocal {
		// The operator typed a HOST path. Inside a container it only becomes
		// reachable once the mount exists, so probe whatever this process can
		// actually see; before the first save there is nothing to probe and the
		// answer is "saving will arrange it".
		probe := hostToContainerPath(req.Path, dockerenv.Running())
		if !dirExists(probe) && dockerenv.Running() {
			return nil
		}
		return assertRepoWritable(probe)
	}
	r := Runner{Bin: bin, Repo: Resolve(req.RepoConfig, RepoSecrets{
		Password: req.Password, S3SecretKey: req.S3SecretKey, SSHPrivateKey: req.SSHPrivateKey,
	}), Port: req.Port}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return r.EnsureRepo(cctx)
}

func (s *service) DeleteRepo(ctx context.Context) error {
	// A node-local filesystem repository is this node's own; removing it also
	// withdraws the mount, which recreates the app.
	if local, err := s.localRepo.Get(ctx); err == nil && local != nil {
		if err := s.localRepo.Delete(ctx); err != nil {
			return err
		}
		_, err := setup.RequestBackupMount(s.dataDir, s.dbType, s.dbDSN, "")
		return err
	}
	if s.raft != nil && s.raft.Enabled() {
		// removeHosts=false: this connector feeds no host rows.
		return s.raft.SubmitConnectorDelete(ctx, connectorTypeAppBackup, ConnectorFingerprint, false)
	}
	return s.connectors.DeleteByFingerprint(ctx, ConnectorFingerprint)
}

func validateRepo(req RepoRequest) error {
	if strings.TrimSpace(req.Password) == "" {
		return errors.New("a repository password is required: restic cannot encrypt without one, and it cannot be recovered")
	}
	switch req.Backend {
	case BackendLocal:
		if !filepath.IsAbs(req.Path) {
			return errors.New("the repository path must be absolute")
		}
	case BackendSFTP:
		if req.Host == "" || req.RemotePath == "" {
			return errors.New("host and remote path are required for an SSH repository")
		}
	case BackendS3:
		if req.Endpoint == "" || req.Bucket == "" {
			return errors.New("endpoint and bucket are required for an S3 repository")
		}
		if req.AccessKeyID == "" || req.S3SecretKey == "" {
			return errors.New("access key and secret are required for an S3 repository")
		}
	default:
		return fmt.Errorf("unknown backend %q", req.Backend)
	}
	return nil
}

// --- per-application planning --------------------------------------------------

func (s *service) Plan(ctx context.Context, hostID uint, project string) (*Plan, error) {
	hostID = s.resolveHost(hostID)
	app, err := s.dockerSvc.GetApplication(ctx, hostID, project)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, fmt.Errorf("application %q not found on host %d", project, hostID)
	}

	dir, files := ResolveProject(app)
	paths := ResolvePaths(app)
	p := &Plan{
		Project: project, HostID: hostID, Local: hostID == s.localHostID,
		ComposeFiles: files, ProjectDir: dir,
		Paths: paths, Services: ResolveServices(app),
	}
	for _, bp := range paths {
		p.TotalSize += bp.Size
	}

	switch {
	case !p.Local:
		p.Blocked = ErrRemoteHost.Error()
	case s.isSelfProject(project):
		p.Blocked = ErrSelfProject.Error()
	case len(files) == 0:
		p.Blocked = "this application has no compose file on disk (it was not deployed with docker compose), so there is nothing to snapshot or rewrite"
	default:
		if st, _ := s.Status(ctx); st != nil && st.Reason != "" {
			p.Blocked = st.Reason
		}
	}
	return p, nil
}

// isSelfProject guards the one project a job must never touch: our own. The
// helper would stop the containers running the app that queued the job.
func (s *service) isSelfProject(project string) bool {
	self := strings.TrimSpace(os.Getenv("NODE_STATS_PROJECT"))
	if self == "" {
		self = "node-stats"
	}
	return strings.EqualFold(project, self)
}

func (s *service) resolveHost(hostID uint) uint {
	if hostID == 0 {
		return s.localHostID
	}
	return hostID
}

func (s *service) Versions(ctx context.Context, image string) ([]string, error) {
	repo, _ := SplitImageRef(image)
	if repo == "" {
		return nil, errors.New("image reference is empty")
	}
	return docker.ListImageTags(ctx, image), nil
}

// --- snapshots -----------------------------------------------------------------

// Snapshots lists a project's snapshots. This runs restic directly in the app
// process: listing is read-only and needs only network access, not the docker
// socket, so it does not have to go through the controller.
func (s *service) Snapshots(ctx context.Context, project string) ([]Snapshot, error) {
	r, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	snaps, err := r.Snapshots(cctx, ProjectTag(project))
	if err == nil {
		// Newest first: the snapshot an operator reaches for is almost always
		// the most recent one, and restic returns them oldest-first.
		sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time.After(snaps[j].Time) })
	}
	if err != nil {
		// An empty repository is not an error to the operator.
		if strings.Contains(err.Error(), "no snapshot") || strings.Contains(err.Error(), "unable to open config") {
			return []Snapshot{}, nil
		}
		return nil, err
	}
	return snaps, nil
}

// RepoStats reports what the repository holds. It is a separate call from
// Status because it walks the repository index: cheap on a small repository,
// seconds on a large one, and the status poll must stay instant.
func (s *service) RepoStats(ctx context.Context) (*RepoStats, error) {
	r, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	st, err := r.Stats(cctx)
	if err != nil {
		// A repository that has never been written to is not an error here —
		// the first backup creates it.
		if strings.Contains(err.Error(), "unable to open config") || strings.Contains(err.Error(), "does not exist") {
			return &RepoStats{}, nil
		}
		return nil, err
	}
	return st, nil
}

func (s *service) DeleteSnapshot(ctx context.Context, id string) error {
	r, err := s.runner(ctx)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	return r.Forget(cctx, id)
}

// appRepoPath is where THIS process reaches the repository: the fixed mount
// inside a container, the host path itself on a native install.
func (s *service) appRepoPath(cfg RepoConfig) string {
	if cfg.Backend != BackendLocal {
		return RepoURL(cfg)
	}
	return hostToContainerPath(cfg.Path, dockerenv.Running())
}

// runner builds a restic runner for THIS process (read-only listing and
// forget). A filesystem repository is a host path: the job helper reaches it
// directly via an identity mount, but the app container only sees the host
// through HOST_ROOT, so the path is translated here.
func (s *service) runner(ctx context.Context) (Runner, error) {
	cfg, sec, err := s.loadRepo(ctx)
	if err != nil {
		return Runner{}, err
	}
	bin, err := EnsureBinary(ctx, s.dataDir)
	if err != nil {
		return Runner{}, fmt.Errorf("install restic: %w", err)
	}
	repo := Resolve(*cfg, *sec)
	repo.URL = s.appRepoPath(*cfg)
	return Runner{Bin: bin, Repo: repo, Port: cfg.Port}, nil
}

// assertRepoWritable verifies that this process can actually write at a
// filesystem repository's location. Remote backends are not checked here —
// EnsureRepo proves their access by opening or creating the repository.
//
// The check writes and removes a probe file rather than reading permission
// bits, because the mount being read-only is exactly the case that matters and
// a read-only bind mount still reports permissive modes.
func assertRepoWritable(path string) error {
	if !filepath.IsAbs(path) {
		return nil
	}
	dir := path
	if !dirExists(dir) {
		dir = filepath.Dir(path)
	}
	if !dirExists(dir) {
		return fmt.Errorf("%w: %s does not exist on this node", ErrRepoReadOnly, dir)
	}
	probe, err := os.CreateTemp(dir, ".node-stats-write-probe-*")
	if err != nil {
		return fmt.Errorf("%w: %s is not writable here%s", ErrRepoReadOnly, dir, hostMountHint(path))
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	return nil
}

// hostMountHint explains the common cause: the path exists on the host but was
// only ever bind-mounted read-only (HOST_ROOT), so node-stats can see it and
// not write it.
func hostMountHint(path string) string {
	root := strings.TrimRight(os.Getenv("HOST_ROOT"), "/")
	if root == "" || root == "/" || strings.HasPrefix(path, root+"/") {
		return ""
	}
	if dirExists(filepath.Join(root, path)) {
		return fmt.Sprintf(" (it exists on the host but node-stats only sees it through the read-only %s mount — bind-mount %s into the app container at the same path, read-write)", root, path)
	}
	return fmt.Sprintf(" (bind-mount %s into the app container at the same path, read-write)", path)
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// --- actions -------------------------------------------------------------------

func (s *service) Backup(ctx context.Context, hostID uint, project string) (*RunEntity, error) {
	return s.enqueue(ctx, hostID, project, JobBackup, nil, "")
}

func (s *service) Update(ctx context.Context, hostID uint, project string, targets []ServiceTarget) (*RunEntity, error) {
	if len(targets) == 0 {
		return nil, errors.New("pick at least one service to update")
	}
	return s.enqueue(ctx, hostID, project, JobUpdate, targets, "")
}

func (s *service) Restore(ctx context.Context, hostID uint, project, snapshotID string) (*RunEntity, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return nil, errors.New("pick a snapshot to restore")
	}
	return s.enqueue(ctx, hostID, project, JobRestore, nil, snapshotID)
}

// enqueue validates, resolves and writes a job for the controller to pick up.
func (s *service) enqueue(ctx context.Context, hostID uint, project, kind string, targets []ServiceTarget, snapshotID string) (*RunEntity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	plan, err := s.Plan(ctx, hostID, project)
	if err != nil {
		return nil, err
	}
	if plan.Blocked != "" {
		return nil, errors.New(plan.Blocked)
	}
	if err := s.assertNotBusy(ctx, plan.HostID, project); err != nil {
		return nil, err
	}

	cfg, sec, err := s.loadRepo(ctx)
	if err != nil {
		return nil, err
	}

	job := Job{
		ID:           newJobID(),
		Kind:         kind,
		Project:      project,
		ProjectDir:   plan.ProjectDir,
		ComposeFiles: plan.ComposeFiles,
		Paths:        plan.Paths,
		Targets:      targets,
		SnapshotID:   snapshotID,
		Repo:         Resolve(*cfg, *sec),
		CreatedAt:    time.Now().UTC(),
	}
	if err := AppendJob(s.dataDir, job); err != nil {
		return nil, fmt.Errorf("queue job: %w", err)
	}

	detail, _ := json.Marshal(map[string]any{"targets": targets, "paths": plan.Paths, "snapshot_id": snapshotID})
	run := &RunEntity{
		JobID: job.ID, HostID: plan.HostID, Project: project, Kind: kind,
		Phase: PhaseQueued, StartedAt: job.CreatedAt, Detail: string(detail),
		Message: "queued",
	}
	if err := s.repo.Upsert(ctx, run); err != nil {
		return nil, err
	}
	s.logger.Info("appbackup: job queued", "id", job.ID, "kind", kind, "project", project, "paths", len(plan.Paths))
	return run, nil
}

// assertNotBusy refuses a second job for the same application: jobs stop
// containers, so two of them would fight over one project.
func (s *service) assertNotBusy(ctx context.Context, hostID uint, project string) error {
	active, err := s.repo.ListActive(ctx)
	if err != nil {
		return err
	}
	for _, r := range active {
		if r.Project == project && r.HostID == hostID {
			return ErrBusy
		}
	}
	return nil
}

func newJobID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// --- history -------------------------------------------------------------------

func (s *service) Runs(ctx context.Context, hostID uint, project string, limit int) ([]RunEntity, error) {
	if err := s.SyncRuns(ctx); err != nil {
		s.logger.Warn("appbackup: syncing run status failed", "error", err)
	}
	return s.repo.ListByProject(ctx, s.resolveHost(hostID), project, limit)
}

// SyncRuns folds the controller's status file into the run history.
func (s *service) SyncRuns(ctx context.Context) error {
	sf, err := ReadStatus(s.dataDir)
	if err != nil || sf == nil {
		return err
	}
	active, err := s.repo.ListActive(ctx)
	if err != nil {
		return err
	}
	for i := range active {
		st, ok := sf.Statuses[active[i].JobID]
		if !ok {
			continue
		}
		run := active[i]
		run.Phase = st.Phase
		run.Message = strings.TrimSpace(st.Step + " " + st.Message)
		run.Error = st.Error
		if st.SnapshotID != "" {
			run.SnapshotID = st.SnapshotID
		}
		if !st.FinishedAt.IsZero() {
			run.FinishedAt = st.FinishedAt
		}
		if len(st.Log) > 0 {
			if b, err := json.Marshal(st.Log); err == nil {
				run.Detail = string(b)
			}
		}
		if err := s.repo.Upsert(ctx, &run); err != nil {
			s.logger.Warn("appbackup: persisting run status failed", "job", run.JobID, "error", err)
		}
	}
	return nil
}
