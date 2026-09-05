package appbackup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"system-stats/internal/platform/setup"
)

// A filesystem repository is node-local by nature: a directory on machine A is
// unreachable from machine B, so replicating its configuration would leave half
// a cluster silently pointing at a path that does not exist there. It therefore
// lives in a local table, outside the Raft snapshot allow-list, while the SSH
// and S3 repositories stay in the replicated connector registry.

// LocalRepoEntity is this node's filesystem repository. At most one row; the
// secret is sealed with the same cipher the connectors use.
type LocalRepoEntity struct {
	ID        uint   `gorm:"primaryKey"`
	Config    string `gorm:"type:text"`
	SecretEnc []byte
}

// TableName returns the database table name for GORM operations.
func (LocalRepoEntity) TableName() string { return "app_backup_local_repo" }

// localRepoID is the fixed primary key: one filesystem repository per node.
const localRepoID uint = 1

// LocalRepoStore persists this node's filesystem repository.
type LocalRepoStore interface {
	Get(ctx context.Context) (*LocalRepoEntity, error)
	Save(ctx context.Context, e *LocalRepoEntity) error
	Delete(ctx context.Context) error
}

type localRepoStore struct{ db *gorm.DB }

// NewLocalRepoStore wires the node-local repository store.
func NewLocalRepoStore(db *gorm.DB) LocalRepoStore { return &localRepoStore{db: db} }

func (s *localRepoStore) Get(ctx context.Context) (*LocalRepoEntity, error) {
	var e LocalRepoEntity
	err := s.db.WithContext(ctx).First(&e, localRepoID).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *localRepoStore) Save(ctx context.Context, e *LocalRepoEntity) error {
	e.ID = localRepoID
	return s.db.WithContext(ctx).Save(e).Error
}

func (s *localRepoStore) Delete(ctx context.Context) error {
	return s.db.WithContext(ctx).Delete(&LocalRepoEntity{}, localRepoID).Error
}

// SuggestedBackupPath is the default offered in the UI: a `backups` directory
// beside the installation, so the operator names a place they recognise instead
// of inventing one.
//
//   - docker: <stack>/backups, next to data/ and .env.agent. The stack's HOST
//     path is what matters — the repository is mounted into the container, and
//     what gets stored is the host path.
//   - native: alongside the data directory, which already is a host path.
//
// Returns "" when running in a container that was not told its stack directory:
// every path this process can see is then a CONTAINER path, and suggesting one
// would invite the operator to store a location that means nothing on the host.
func SuggestedBackupPath(dataDir string, inContainer bool) string {
	if stack := strings.TrimSpace(os.Getenv("NODE_STATS_STACK_HOST_DIR")); stack != "" {
		return filepath.Join(stack, "backups")
	}
	if inContainer {
		return ""
	}
	if d := strings.TrimSpace(dataDir); d != "" && d != "/" {
		return filepath.Join(filepath.Dir(d), "backups")
	}
	return "/var/lib/node-stats/backups"
}

// hostToContainerPath maps the stored HOST path onto the fixed in-container
// mount. Inside a container the repository is always at BackupMountPath; on a
// native install the host path IS the path, so it is returned unchanged.
func hostToContainerPath(hostPath string, inContainer bool) string {
	if !inContainer || strings.TrimSpace(hostPath) == "" {
		return hostPath
	}
	return setup.BackupMountPath
}

// marshalLocalRepo seals a filesystem repository for storage.
func marshalLocalRepo(cfg RepoConfig, sec RepoSecrets, cipher interface {
	Encrypt(string) ([]byte, error)
}) (*LocalRepoEntity, error) {
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	secJSON, err := json.Marshal(sec)
	if err != nil {
		return nil, err
	}
	enc, err := cipher.Encrypt(string(secJSON))
	if err != nil {
		return nil, err
	}
	return &LocalRepoEntity{Config: string(cfgJSON), SecretEnc: enc}, nil
}
