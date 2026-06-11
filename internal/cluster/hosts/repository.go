package hosts

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// syntheticMAC derives a stable locally-administered placeholder MAC for a
// connector row whose real MAC is unknown or already owned by another host
// row (docker-bridge MACs collide across sites). b2 sets the LAA bit so it
// can never clash with real hardware.
func syntheticMAC(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("b2:%02x:%02x:%02x:%02x:%02x", sum[0], sum[1], sum[2], sum[3], sum[4])
}

// Repository defines the interface for host data operations.
type Repository interface {
	// UpsertLocalHost updates the fixed local collector row (id = LocalCollectorHostID).
	UpsertLocalHost(ctx context.Context, hostInfo HostInfo) (*Host, error)
	UpsertHost(ctx context.Context, hostInfo HostInfo) (*Host, error)
	GetHostByMacAddress(ctx context.Context, macAddress string) (*Host, error)
	GetHostByID(ctx context.Context, id uint) (*Host, error)
	GetAllHosts(ctx context.Context) ([]Host, error)
	UpdateLastSeen(ctx context.Context, hostID uint) error
	// UpdateLastSeenAndAgentSession updates last_seen and agent_session_started_at (for node push heartbeats).
	UpdateLastSeenAndAgentSession(ctx context.Context, hostID uint, lastSeen time.Time, agentSessionStarted *time.Time) error
	// UpdateHostLabelsFromAgentPush updates name and/or ipv4 from cluster agent push (non-empty values only). Skips local collector id.
	UpdateHostLabelsFromAgentPush(ctx context.Context, hostID uint, name, ipv4 string) error
	// DeleteHostCascade removes a host row, node credentials, and all stored metrics scoped to that host_id.
	DeleteHostCascade(ctx context.Context, hostID uint) error
	// UpsertConnectorHost creates/updates a connector-fed host row (hypervisor
	// node or agent-less guest). Matches by ExternalID, then MAC — never by
	// name, so a connector can't hijack an agent row that happens to share a
	// hostname. When the MAC matches an agent-maintained row, only topology
	// fields are attached (the agent keeps owning everything else).
	UpsertConnectorHost(ctx context.Context, info ConnectorHostInfo) (*Host, error)
	// GetHostByExternalID resolves a host by its connector-side identity.
	GetHostByExternalID(ctx context.Context, externalID string) (*Host, error)
	// GetHostByOriginAndName resolves a replicated host by its origin site and
	// name — the fallback when MAC identity is ambiguous across sites (docker
	// bridge containers on different machines derive identical 02:42:… MACs).
	GetHostByOriginAndName(ctx context.Context, origin, name string) (*Host, error)
	// GetHostByHardwareUUID resolves a host by its SMBIOS product UUID — the
	// VM linking key when the agent's registered MAC is absent from the
	// guest's PVE config (agent behind a Docker bridge).
	GetHostByHardwareUUID(ctx context.Context, uuid string) (*Host, error)
	// GetUnlinkedAgentHostByName resolves an agent-maintained row that is not
	// yet attached to any hypervisor — the weakest linking key (a PVE guest
	// name usually equals the hostname the agent registered with).
	GetUnlinkedAgentHostByName(ctx context.Context, name string) (*Host, error)
	// FindConnectorOnlyHostsByExternalIDSuffix lists connector-owned rows whose
	// external_id ends with suffix (e.g. "/lxc/105") — used by the self-link
	// flow to find this node's own guest row across configured connectors.
	FindConnectorOnlyHostsByExternalIDSuffix(ctx context.Context, suffix string) ([]Host, error)
	// UnlinkConnectorHosts detaches every host whose external_id starts with
	// prefix: agent rows lose their topology fields; connector-only rows are
	// cascade-deleted when removeHosts is true, otherwise kept (frozen).
	UnlinkConnectorHosts(ctx context.Context, prefix string, removeHosts bool) error
}

type hostRepository struct {
	db *gorm.DB
}

// NewRepository creates a new host repository.
func NewRepository(db *gorm.DB) Repository {
	return &hostRepository{db: db}
}

// reclaimDuplicateLocalRows removes legacy host rows that share this machine's
// MAC or name but sit at a non-reserved id, so the local collector row (id=1)
// can be created without violating the unique indexes.
func (r *hostRepository) reclaimDuplicateLocalRows(ctx context.Context, hostInfo HostInfo) error {
	for _, q := range []struct {
		col string
		val string
	}{
		{"mac_address", hostInfo.MacAddress},
		{"name", hostInfo.Name},
	} {
		if q.val == "" {
			continue
		}
		var h Host
		err := r.db.WithContext(ctx).
			Where(q.col+" = ? AND id != ?", q.val, LocalCollectorHostID).
			First(&h).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err := r.DeleteHostCascade(ctx, h.ID); err != nil {
			return err
		}
	}
	return nil
}

func (r *hostRepository) UpsertLocalHost(ctx context.Context, hostInfo HostInfo) (*Host, error) {
	var host Host
	id := LocalCollectorHostID
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&host).Error
	now := time.Now()
	if err == gorm.ErrRecordNotFound {
		if err := r.reclaimDuplicateLocalRows(ctx, hostInfo); err != nil {
			return nil, err
		}
		host = Host{
			ID:                   id,
			Name:                 hostInfo.Name,
			MacAddress:           hostInfo.MacAddress,
			IPv4:                 hostInfo.IPv4,
			OS:                   hostInfo.OS,
			Platform:             hostInfo.Platform,
			PlatformFamily:       hostInfo.PlatformFamily,
			PlatformVersion:      hostInfo.PlatformVersion,
			KernelVersion:        hostInfo.KernelVersion,
			VirtualizationSystem: hostInfo.VirtualizationSystem,
			VirtualizationRole:   hostInfo.VirtualizationRole,
			SystemHostID:         hostInfo.HostID,
			HardwareUUID:         hostInfo.HardwareUUID,
			Source:               SourceAgent,
			BootTime:             hostInfo.BootTime,
			LastSeen:             now,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		return &host, r.db.WithContext(ctx).Create(&host).Error
	}
	if err != nil {
		return nil, err
	}
	host.Name = hostInfo.Name
	host.MacAddress = hostInfo.MacAddress
	host.IPv4 = hostInfo.IPv4
	host.OS = hostInfo.OS
	host.Platform = hostInfo.Platform
	host.PlatformFamily = hostInfo.PlatformFamily
	host.PlatformVersion = hostInfo.PlatformVersion
	host.KernelVersion = hostInfo.KernelVersion
	host.VirtualizationSystem = hostInfo.VirtualizationSystem
	host.VirtualizationRole = hostInfo.VirtualizationRole
	host.SystemHostID = hostInfo.HostID
	host.HardwareUUID = hostInfo.HardwareUUID
	host.Source = MergeSource(host.Source, SourceAgent)
	host.BootTime = hostInfo.BootTime
	host.LastSeen = now
	host.UpdatedAt = now
	return &host, r.db.WithContext(ctx).Save(&host).Error
}

func (r *hostRepository) UpsertHost(ctx context.Context, hostInfo HostInfo) (*Host, error) {
	var host Host

	// 1) Try to find by MAC address (never match the reserved local collector row)
	err := r.db.WithContext(ctx).
		Where("mac_address = ? AND id != ?", hostInfo.MacAddress, LocalCollectorHostID).
		First(&host).Error
	if err == nil {
		// Found by MAC → update fields and timestamps
		now := time.Now()
		host.Name = hostInfo.Name
		host.IPv4 = hostInfo.IPv4
		host.OS = hostInfo.OS
		host.Platform = hostInfo.Platform
		host.PlatformFamily = hostInfo.PlatformFamily
		host.PlatformVersion = hostInfo.PlatformVersion
		host.KernelVersion = hostInfo.KernelVersion
		host.VirtualizationSystem = hostInfo.VirtualizationSystem
		host.VirtualizationRole = hostInfo.VirtualizationRole
		host.SystemHostID = hostInfo.HostID
		host.HardwareUUID = hostInfo.HardwareUUID
		host.OriginCluster = hostInfo.OriginCluster
		host.Source = MergeSource(host.Source, SourceAgent)
		host.BootTime = hostInfo.BootTime
		host.LastSeen = now
		host.UpdatedAt = now
		return &host, r.db.WithContext(ctx).Save(&host).Error
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// 2) Not found by MAC → try to find by Name. A bare name match is only
	// trusted as "same machine, its MAC changed" when a stable hardware
	// identity corroborates it — different machines on different sites
	// (uplinked clusters) often share default hostnames, and merging them
	// would flip the row's MAC back and forth every cycle.
	var hostByName Host
	err = r.db.WithContext(ctx).
		Where("name = ? AND id != ?", hostInfo.Name, LocalCollectorHostID).
		First(&hostByName).Error
	if err == nil && !sameMachineIdentity(&hostByName, hostInfo) {
		err = gorm.ErrRecordNotFound
	}
	if err == nil {
		// Found by Name → update fields and timestamps
		now := time.Now()
		hostByName.MacAddress = hostInfo.MacAddress
		hostByName.IPv4 = hostInfo.IPv4
		hostByName.OS = hostInfo.OS
		hostByName.Platform = hostInfo.Platform
		hostByName.PlatformFamily = hostInfo.PlatformFamily
		hostByName.PlatformVersion = hostInfo.PlatformVersion
		hostByName.KernelVersion = hostInfo.KernelVersion
		hostByName.VirtualizationSystem = hostInfo.VirtualizationSystem
		hostByName.VirtualizationRole = hostInfo.VirtualizationRole
		hostByName.SystemHostID = hostInfo.HostID
		hostByName.HardwareUUID = hostInfo.HardwareUUID
		hostByName.OriginCluster = hostInfo.OriginCluster
		hostByName.Source = MergeSource(hostByName.Source, SourceAgent)
		hostByName.BootTime = hostInfo.BootTime
		hostByName.LastSeen = now
		hostByName.UpdatedAt = now
		return &hostByName, r.db.WithContext(ctx).Save(&hostByName).Error
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// 2b) Guard against duplicating this node's own local collector row.
	// The local host lives at id=1 and is excluded from the lookups above so
	// a remote join never overwrites it. But every node also replicates its
	// own local host into the cluster registry (so peers see it in /hosts),
	// and that command is applied on the origin node too. There, the MAC/Name
	// match only id=1 — which we skipped — so without this guard we'd fall
	// through to CREATE and hit `UNIQUE constraint failed: hosts.name`. When
	// id=1 already represents this host, it's our own machine: return that row
	// untouched instead of inserting a duplicate.
	var localHost Host
	err = r.db.WithContext(ctx).
		Where("id = ? AND (mac_address = ? OR name = ?)", LocalCollectorHostID, hostInfo.MacAddress, hostInfo.Name).
		First(&localHost).Error
	if err == nil {
		return &localHost, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// 3) No (corroborated) match → create. The name may still be taken by a
	// DIFFERENT machine (hosts.name is unique) — suffix with the MAC tail.
	freeName, nerr := r.resolveFreeName(ctx, hostInfo.Name, 0, macTail(hostInfo.MacAddress))
	if nerr != nil {
		return nil, nerr
	}
	now := time.Now()
	host = Host{
		Name:                 freeName,
		MacAddress:           hostInfo.MacAddress,
		IPv4:                 hostInfo.IPv4,
		OS:                   hostInfo.OS,
		Platform:             hostInfo.Platform,
		PlatformFamily:       hostInfo.PlatformFamily,
		PlatformVersion:      hostInfo.PlatformVersion,
		KernelVersion:        hostInfo.KernelVersion,
		VirtualizationSystem: hostInfo.VirtualizationSystem,
		VirtualizationRole:   hostInfo.VirtualizationRole,
		SystemHostID:         hostInfo.HostID,
		HardwareUUID:         hostInfo.HardwareUUID,
		OriginCluster:        hostInfo.OriginCluster,
		Source:               SourceAgent,
		BootTime:             hostInfo.BootTime,
		LastSeen:             now,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	return &host, r.db.WithContext(ctx).Create(&host).Error
}

// sameMachineIdentity reports whether a name-matched row is genuinely the
// same machine: the stable ids (machine-id or SMBIOS UUID) must agree.
func sameMachineIdentity(existing *Host, info HostInfo) bool {
	if info.HostID != "" && existing.SystemHostID == info.HostID {
		return true
	}
	if info.HardwareUUID != "" && existing.HardwareUUID == info.HardwareUUID {
		return true
	}
	return false
}

// macTail is a short, human-readable disambiguation suffix ("a0:56").
func macTail(mac string) string {
	if len(mac) >= 5 {
		return mac[len(mac)-5:]
	}
	return mac
}

// connectorHostAlive reports whether a connector-fed row should have its
// last_seen refreshed: the hypervisor says the machine is powered on.
func connectorHostAlive(status string) bool {
	return status == "running" || status == "online"
}

// resolveFreeName returns desired if no OTHER row holds it, otherwise a
// deterministic suffixed variant (hosts.name is unique; a PVE guest may share
// a hostname with an unrelated registered machine).
func (r *hostRepository) resolveFreeName(ctx context.Context, desired string, selfID uint, externalID string) (string, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Host{}).
		Where("name = ? AND id != ?", desired, selfID).
		Count(&count).Error
	if err != nil {
		return "", err
	}
	if count == 0 {
		return desired, nil
	}
	// e.g. "media-vm [pve/qemu/105]" — stable, so retries converge.
	suffix := externalID
	if i := strings.Index(suffix, ":"); i >= 0 {
		suffix = suffix[i+1:]
	}
	if i := strings.Index(suffix, "/"); i >= 0 {
		suffix = suffix[i+1:]
	}
	return fmt.Sprintf("%s [%s]", desired, suffix), nil
}

func (r *hostRepository) UpsertConnectorHost(ctx context.Context, info ConnectorHostInfo) (*Host, error) {
	now := time.Now()

	var host Host
	err := r.db.WithContext(ctx).
		Where("external_id = ? AND external_id != ''", info.ExternalID).
		First(&host).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && info.MacAddress != "" {
		err = r.db.WithContext(ctx).
			Where("mac_address = ?", info.MacAddress).
			First(&host).Error
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Cross-site guard: a replicated (remote-origin) connector row must never
	// claim THIS node's own collector row. Docker-bridge agents on different
	// machines derive identical MACs (02:42:… from equal bridge IPs), so a
	// remote spoke's self-linked guest can resolve to OUR identity row and
	// attach a foreign hypervisor topology to it. Treat the match as absent;
	// when our row carries exactly the incoming external_id, that topology
	// could only have been written by this same collision — shed it.
	if err == nil && host.ID == LocalCollectorHostID && info.OriginCluster != "" {
		if host.ExternalID != "" && host.ExternalID == info.ExternalID {
			if uerr := r.db.WithContext(ctx).Model(&Host{}).
				Where("id = ?", host.ID).
				Updates(map[string]interface{}{
					"host_type":    "",
					"parent_mac":   "",
					"external_id":  "",
					"guest_status": "",
					"source":       SourceAgent,
					"updated_at":   now,
				}).Error; uerr != nil {
				return nil, uerr
			}
		}
		host = Host{}
		err = gorm.ErrRecordNotFound
	}

	if err == nil {
		merged := MergeSource(host.Source, SourceConnector)
		if merged != SourceConnector {
			// Agent-maintained row: only attach topology, via a column-limited
			// update so a concurrent agent upsert is never clobbered.
			return &host, r.db.WithContext(ctx).Model(&Host{}).
				Where("id = ?", host.ID).
				Updates(map[string]interface{}{
					"host_type":      info.HostType,
					"parent_mac":     info.ParentMAC,
					"external_id":    info.ExternalID,
					"guest_status":   info.GuestStatus,
					"origin_cluster": info.OriginCluster,
					"source":         merged,
					"updated_at":     now,
				}).Error
		}
		// Connector-owned row: the connector is the only writer, refresh everything.
		name, nerr := r.resolveFreeName(ctx, info.Name, host.ID, info.ExternalID)
		if nerr != nil {
			return nil, nerr
		}
		host.Source = merged
		host.HostType = info.HostType
		host.ParentMAC = info.ParentMAC
		host.ExternalID = info.ExternalID
		host.GuestStatus = info.GuestStatus
		host.OriginCluster = info.OriginCluster
		host.Name = name
		host.MacAddress = info.MacAddress
		host.IPv4 = info.IPv4
		host.OS = info.OS
		host.Platform = info.Platform
		host.PlatformFamily = info.PlatformFamily
		host.PlatformVersion = info.PlatformVersion
		host.KernelVersion = info.KernelVersion
		host.BootTime = info.BootTime
		host.UpdatedAt = now
		if connectorHostAlive(info.GuestStatus) {
			host.LastSeen = now
		}
		return &host, r.db.WithContext(ctx).Save(&host).Error
	}

	name, nerr := r.resolveFreeName(ctx, info.Name, 0, info.ExternalID)
	if nerr != nil {
		return nil, nerr
	}
	// mac_address is unique: when the desired MAC is already owned by another
	// row (cross-site docker-bridge collision deflected above) or absent,
	// derive a stable locally-administered placeholder — connector rows are
	// keyed by external_id anyway.
	mac := info.MacAddress
	if mac != "" {
		var taken int64
		if cerr := r.db.WithContext(ctx).Model(&Host{}).
			Where("mac_address = ?", mac).Count(&taken).Error; cerr != nil {
			return nil, cerr
		}
		if taken > 0 {
			mac = ""
		}
	}
	if mac == "" {
		mac = syntheticMAC(info.OriginCluster, info.ExternalID, info.Name)
	}
	host = Host{
		Name:            name,
		MacAddress:      mac,
		IPv4:            info.IPv4,
		OS:              info.OS,
		Platform:        info.Platform,
		PlatformFamily:  info.PlatformFamily,
		PlatformVersion: info.PlatformVersion,
		KernelVersion:   info.KernelVersion,
		HostType:        info.HostType,
		ParentMAC:       info.ParentMAC,
		Source:          SourceConnector,
		ExternalID:      info.ExternalID,
		GuestStatus:     info.GuestStatus,
		OriginCluster:   info.OriginCluster,
		BootTime:        info.BootTime,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if connectorHostAlive(info.GuestStatus) {
		host.LastSeen = now
	}
	return &host, r.db.WithContext(ctx).Create(&host).Error
}

func (r *hostRepository) GetHostByExternalID(ctx context.Context, externalID string) (*Host, error) {
	if externalID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var host Host
	err := r.db.WithContext(ctx).Where("external_id = ?", externalID).First(&host).Error
	if err != nil {
		return nil, err
	}
	return &host, nil
}

func (r *hostRepository) GetHostByOriginAndName(ctx context.Context, origin, name string) (*Host, error) {
	if origin == "" || name == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var host Host
	err := r.db.WithContext(ctx).
		Where("origin_cluster = ? AND name = ?", origin, name).
		First(&host).Error
	if err != nil {
		return nil, err
	}
	return &host, nil
}

func (r *hostRepository) GetHostByHardwareUUID(ctx context.Context, uuid string) (*Host, error) {
	if uuid == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var host Host
	err := r.db.WithContext(ctx).Where("hardware_uuid = ?", uuid).First(&host).Error
	if err != nil {
		return nil, err
	}
	return &host, nil
}

func (r *hostRepository) GetUnlinkedAgentHostByName(ctx context.Context, name string) (*Host, error) {
	if name == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var host Host
	err := r.db.WithContext(ctx).
		Where("name = ? AND source != ? AND (external_id = '' OR external_id IS NULL)",
			name, SourceConnector).
		First(&host).Error
	if err != nil {
		return nil, err
	}
	return &host, nil
}

func (r *hostRepository) FindConnectorOnlyHostsByExternalIDSuffix(ctx context.Context, suffix string) ([]Host, error) {
	var hosts []Host
	err := r.db.WithContext(ctx).
		Where("external_id LIKE ? AND source = ?", "%"+suffix, SourceConnector).
		Find(&hosts).Error
	return hosts, err
}

func (r *hostRepository) UnlinkConnectorHosts(ctx context.Context, prefix string, removeHosts bool) error {
	if prefix == "" {
		return nil
	}
	like := prefix + "%"
	if removeHosts {
		var orphans []Host
		err := r.db.WithContext(ctx).
			Where("external_id LIKE ? AND source = ? AND id != ?", like, SourceConnector, LocalCollectorHostID).
			Find(&orphans).Error
		if err != nil {
			return err
		}
		for _, h := range orphans {
			if err := r.DeleteHostCascade(ctx, h.ID); err != nil {
				return err
			}
		}
	}
	// Every remaining linked row (agent+connector, or kept connector-only)
	// loses its topology; agents keep running as plain top-level hosts.
	return r.db.WithContext(ctx).Model(&Host{}).
		Where("external_id LIKE ?", like).
		Updates(map[string]interface{}{
			"host_type":    "",
			"parent_mac":   "",
			"external_id":  "",
			"guest_status": "",
			"source":       gorm.Expr("CASE WHEN source = ? THEN ? ELSE ? END", SourceConnector, SourceConnector, SourceAgent),
			"updated_at":   time.Now(),
		}).Error
}

func (r *hostRepository) GetHostByMacAddress(ctx context.Context, macAddress string) (*Host, error) {
	var host Host
	err := r.db.WithContext(ctx).Where("mac_address = ?", macAddress).First(&host).Error
	if err != nil {
		return nil, err
	}
	return &host, nil
}

func (r *hostRepository) GetHostByID(ctx context.Context, id uint) (*Host, error) {
	var host Host
	err := r.db.WithContext(ctx).First(&host, id).Error
	if err != nil {
		return nil, err
	}
	return &host, nil
}

func (r *hostRepository) GetAllHosts(ctx context.Context) ([]Host, error) {
	var hosts []Host
	// Local collector first, then others by id (stable UX).
	err := r.db.WithContext(ctx).
		Order(fmt.Sprintf("CASE WHEN id = %d THEN 0 ELSE 1 END, id ASC", LocalCollectorHostID)).
		Find(&hosts).Error
	return hosts, err
}

func (r *hostRepository) UpdateLastSeen(ctx context.Context, hostID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&Host{}).
		Where("id = ?", hostID).
		Updates(map[string]interface{}{"last_seen": now, "updated_at": now}).Error
}

func (r *hostRepository) UpdateLastSeenAndAgentSession(ctx context.Context, hostID uint, lastSeen time.Time, agentSessionStarted *time.Time) error {
	updates := map[string]interface{}{
		"last_seen":  lastSeen,
		"updated_at": lastSeen,
	}
	if agentSessionStarted != nil {
		updates["agent_session_started_at"] = *agentSessionStarted
	}
	return r.db.WithContext(ctx).Model(&Host{}).
		Where("id = ?", hostID).
		Updates(updates).Error
}

func (r *hostRepository) UpdateHostLabelsFromAgentPush(ctx context.Context, hostID uint, name, ipv4 string) error {
	if hostID == LocalCollectorHostID {
		return nil
	}
	updates := map[string]interface{}{}
	if n := strings.TrimSpace(name); n != "" {
		updates["name"] = n
	}
	if ip := strings.TrimSpace(ipv4); ip != "" {
		updates["ipv4"] = ip
	}
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now().UTC()
	return r.db.WithContext(ctx).Model(&Host{}).
		Where("id = ?", hostID).
		Updates(updates).Error
}

func (r *hostRepository) DeleteHostCascade(ctx context.Context, hostID uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Metrics tables (raw SQL to avoid an import cycle with the metrics
		// packages). Statement errors MUST be checked: one failed statement
		// aborts the transaction, and ignoring it lets the final commit fail with
		// the opaque "commit unexpectedly resulted in rollback". Child docker rows
		// live in docker_container_entities (GORM's default name for
		// DockerContainerEntity), deleted before their parent docker_metrics.
		stmts := []string{
			"DELETE FROM cpu_metrics WHERE host_id = ?",
			"DELETE FROM memory_metrics WHERE host_id = ?",
			"DELETE FROM disk_metrics WHERE host_id = ?",
			"DELETE FROM network_metrics WHERE host_id = ?",
			"DELETE FROM docker_container_entities WHERE metric_timestamp IN (SELECT timestamp FROM docker_metrics WHERE host_id = ?)",
			"DELETE FROM docker_metrics WHERE host_id = ?",
			"DELETE FROM hosts WHERE id = ?",
		}
		for _, q := range stmts {
			if err := tx.Exec(q, hostID).Error; err != nil {
				return fmt.Errorf("cascade delete host %d (%q): %w", hostID, q, err)
			}
		}
		return nil
	})
}
