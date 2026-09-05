// Package appbackup implements per-application backup, image update and
// restore for the compose projects node-stats already discovers ("applications").
//
// Division of labour, mirroring the wizard↔controller split:
//
//   - the app (this package) knows WHAT to do: it resolves a compose project to
//     its config files, its volumes and bind mounts (docker.DockerMount, already
//     collected cluster-wide), decrypts the restic repository credentials and
//     writes a Job descriptor to the shared data volume;
//   - the controller sidecar knows HOW: it is the only component holding a
//     read-write docker socket, so it runs `docker compose` and restic.
//
// The application container mounts the docker socket read-only
// (setup.BuildComposeContent), so a deployment without a controller cannot run
// these jobs at all — the UI still offers the actions and explains why they are
// unavailable rather than hiding them.
//
// Backups are plain restic snapshots. There is no retention policy: snapshots
// accumulate and are removed explicitly, so the restore list is exactly what
// the operator created.
package appbackup

import "time"

// Repository backends. These map onto the restic repository URL forms restic
// understands natively; PBS is deliberately absent (it speaks only its own
// protocol and ships no arm64 client).
const (
	// BackendLocal is a filesystem path ON THE NODE ITSELF. It is the least
	// useful target — it dies with the machine it protects — so the UI warns
	// loudly before accepting it.
	BackendLocal = "local"
	// BackendSFTP is any SSH host, which is also how a Synology NAS is used as
	// a target (restic: sftp:user@host:/path).
	BackendSFTP = "sftp"
	// BackendS3 is any S3-compatible object store (restic: s3:endpoint/bucket).
	BackendS3 = "s3"
)

// ConnectorFingerprint is the fixed identity of the single cluster-wide backup
// repository connector. One repository per cluster: snapshots carry the host
// name as a restic tag, so all nodes dedup against each other.
const ConnectorFingerprint = "appbackup"

// RepoConfig is the non-secret half of the repository definition, stored as
// JSON in Connector.Config (which replicates through Raft in the clear).
type RepoConfig struct {
	Backend string `json:"backend"`

	// Local
	Path string `json:"path,omitempty"`

	// SFTP
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	User       string `json:"user,omitempty"`
	RemotePath string `json:"remote_path,omitempty"`

	// NoPassword creates and opens the repository WITHOUT encryption, so its
	// snapshots can be restored with nothing but restic — no key to keep, no
	// key to lose. It is a property of the repository at creation time: restic
	// requires --insecure-no-password on every command against it.
	//
	// Deliberately an explicit flag rather than "the password field is empty":
	// backups contain whatever the applications hold — password-manager data,
	// .env files, database contents — and that is not something to leave
	// readable by accident.
	NoPassword bool `json:"no_password,omitempty"`

	// S3
	Endpoint    string `json:"endpoint,omitempty"`
	Bucket      string `json:"bucket,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
	Region      string `json:"region,omitempty"`
	AccessKeyID string `json:"access_key_id,omitempty"`
}

// RepoSecrets is the encrypted half. It is marshalled to JSON and sealed into
// Connector.SecretEnc with the same AES-GCM cipher the other connectors use,
// because the row travels through the Raft log and its snapshots.
type RepoSecrets struct {
	// Password is the restic repository password — without it the repository
	// is unreadable, including by us.
	Password string `json:"password"`
	// S3SecretKey pairs with RepoConfig.AccessKeyID.
	S3SecretKey string `json:"s3_secret_key,omitempty"`
	// SSHPrivateKey is the key restic's sftp backend authenticates with.
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
}

// Job kinds.
const (
	// JobBackup snapshots the project's compose files and data without
	// touching the running containers beyond a stop/start cycle.
	JobBackup = "backup"
	// JobUpdate snapshots, rewrites the image tags in the compose file
	// (keeping a .bak), pulls and brings the project back up. It does NOT
	// judge the result: the operator looks at the app and decides.
	JobUpdate = "update"
	// JobRestore wipes the project's current data and restores a snapshot.
	JobRestore = "restore"
)

// Job phases reported back through JobStatus.
const (
	PhaseQueued    = "queued"
	PhaseRunning   = "running"
	PhaseSucceeded = "succeeded"
	PhaseFailed    = "failed"
)

// BackupPath is one thing worth preserving: a named volume's data directory or
// a bind-mounted host directory. Derived from docker.DockerMount, which the
// docker collector already stores per container.
type BackupPath struct {
	// Kind is "volume" or "bind" — restic treats them identically, but the
	// restore step needs to know whether wiping means "docker volume rm" or
	// "empty this directory".
	Kind string `json:"kind"`
	// Name is the docker volume name (volumes only).
	Name string `json:"name,omitempty"`
	// Source is the path on the host that actually holds the bytes.
	Source string `json:"source"`
	// Services lists the compose services that mount it, for the UI.
	Services []string `json:"services,omitempty"`
	// Size is the last known size in bytes (0 when unknown).
	Size int64 `json:"size,omitempty"`
}

// ServiceTarget is one service's image move in a JobUpdate: from the digest it
// runs now to the tag the operator picked. Direction is recorded so the
// controller can write an honest .bak comment and the UI can warn.
type ServiceTarget struct {
	Service string `json:"service"`
	// CurrentImage is the image reference as written in the compose file.
	CurrentImage string `json:"current_image"`
	// CurrentDigest is the running image's repo digest, recorded so a restore
	// can pin the exact bits rather than a moving tag.
	CurrentDigest string `json:"current_digest,omitempty"`
	// TargetImage is the full reference to move to (repo:tag).
	TargetImage string `json:"target_image"`
}

// Job is the app→controller request. It carries the resolved repository
// credentials because the controller has no database access; the file is
// written 0600 on the shared data volume, exactly as desired-state.json
// already carries the database DSN.
type Job struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`

	// Project is the compose project name, ProjectDir its working directory
	// and ComposeFiles the config files, all taken from the compose labels the
	// docker collector reads.
	Project      string   `json:"project"`
	ProjectDir   string   `json:"project_dir"`
	ComposeFiles []string `json:"compose_files"`

	Paths []BackupPath `json:"paths"`

	// Targets is set for JobUpdate.
	Targets []ServiceTarget `json:"targets,omitempty"`
	// SnapshotID is set for JobRestore.
	SnapshotID string `json:"snapshot_id,omitempty"`

	Repo      ResolvedRepo `json:"repo"`
	CreatedAt time.Time    `json:"created_at"`
}

// ResolvedRepo is everything the controller needs to talk to restic: the
// repository URL and the environment restic reads credentials from.
type ResolvedRepo struct {
	// URL is the restic repository, e.g. "sftp:backup@nas:/volume1/restic".
	URL string `json:"url"`
	// Env are extra environment variables (RESTIC_PASSWORD, AWS_*).
	Env map[string]string `json:"env,omitempty"`
	// SSHPrivateKey, when set, is written to a temp file and passed to restic's
	// sftp backend via -o sftp.args.
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
	// NoPassword marks an unencrypted repository; every restic command against
	// it must carry --insecure-no-password.
	NoPassword bool `json:"no_password,omitempty"`
}

// JobStatus is the controller→app result for a single job.
type JobStatus struct {
	ID      string `json:"id"`
	Phase   string `json:"phase"`
	Step    string `json:"step,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	// SnapshotID is the restic snapshot a backup/update produced.
	SnapshotID string    `json:"snapshot_id,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	// Log is the tail of command output, bounded, for the UI.
	Log []string `json:"log,omitempty"`
}

// SnapshotMove is one service's version change recorded on an update
// snapshot, so the history says WHAT the update did rather than only what the
// project looked like before it.
type SnapshotMove struct {
	Service string `json:"service"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// RepoStats is the repository as a whole: what the snapshots actually cost on
// the far end. Surfaced on the repository card so the size of the backups is
// visible without opening an application.
type RepoStats struct {
	TotalSize        int64   `json:"total_size"`
	UncompressedSize int64   `json:"uncompressed_size"`
	CompressionRatio float64 `json:"compression_ratio"`
	SnapshotCount    int     `json:"snapshot_count"`
}

// Snapshot is one restic snapshot as presented to the UI.
type Snapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Project  string    `json:"project"`
	Kind     string    `json:"kind"`
	Paths    []string  `json:"paths"`
	Tags     []string  `json:"tags"`
	// Images records what each service ran at snapshot time, so a restore can
	// put the compose file back the way it was.
	Images map[string]string `json:"images,omitempty"`
	// Moves is set on an update snapshot: the version changes it preceded.
	Moves []SnapshotMove `json:"moves,omitempty"`
	// Size is the data the snapshot holds (its restore size); SizeAdded is what
	// it newly cost the repository after deduplication.
	Size      int64 `json:"size,omitempty"`
	SizeAdded int64 `json:"size_added,omitempty"`
}

// RunEntity is the local history row. It is regenerable operational state, not
// a cluster record: it stays out of Raft and out of the DB-switch dump, in line
// with the project's "regenerable caches live only in the database" rule.
type RunEntity struct {
	ID         uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	JobID      string    `json:"job_id" gorm:"uniqueIndex;not null"`
	HostID     uint      `json:"host_id" gorm:"index"`
	Project    string    `json:"project" gorm:"index"`
	Kind       string    `json:"kind"`
	Phase      string    `json:"phase"`
	SnapshotID string    `json:"snapshot_id,omitempty"`
	Message    string    `json:"message,omitempty"`
	Error      string    `json:"error,omitempty"`
	Detail     string    `json:"-" gorm:"type:text"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName returns the database table name for GORM operations.
func (RunEntity) TableName() string { return "app_backup_runs" }

// Result is what the helper container writes back for the controller to read.
// A non-empty Error means the job failed; SnapshotID may still be set when the
// snapshot itself succeeded and a later step did not.
type Result struct {
	JobID      string    `json:"job_id"`
	SnapshotID string    `json:"snapshot_id,omitempty"`
	Error      string    `json:"error,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}
