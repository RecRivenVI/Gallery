package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

// maxDeferredPublicationLeases 限制 full VACUUM 窗口内尚未落盘的 lease 数量。
// 该值属于 PRE_FREEZE 维护防御预算；达到上限时拒绝新的首屏/显式快照读取，不能让
// 单次维护把进程内存变成无界队列。
const maxDeferredPublicationLeases = 16_384

type deferredPublicationLease struct {
	id                     string
	publicationID          string
	authorizationScopeHash string
	expiresAt              int64
	createdAt              int64
}

// PublicationLeaseCoordinator 只在 Catalog full VACUUM 持有 SQLite 写者期间暂存
// query publication lease。读取仍直接使用 WAL 中的冻结 publication；VACUUM 返回后，
// 所有尚未关闭的 lease 必须先在一个事务中持久化，维护锁才可以释放。正常运行路径
// 仍直接写 catalog.db，不改变 publication 或普通查询的存储布局。
type PublicationLeaseCoordinator struct {
	db *sql.DB

	mu              sync.Mutex
	deferred        bool
	pending         map[string]deferredPublicationLease
	pendingDeletes  map[string]struct{}
	maxPendingLease int
}

func newPublicationLeaseCoordinator(db *sql.DB) *PublicationLeaseCoordinator {
	return &PublicationLeaseCoordinator{
		db:              db,
		pending:         make(map[string]deferredPublicationLease),
		pendingDeletes:  make(map[string]struct{}),
		maxPendingLease: maxDeferredPublicationLeases,
	}
}

// BeginDeferred 在 maintenance.Coordinator 的独占区间内调用。它会等待已经开始的
// 普通 lease 写入结束，再切换到内存暂存，因此 full VACUUM 不会和迟到写者竞争。
// 若上一轮持久化失败而仍处于 deferred，调用保持幂等，让后续维护可以再次收敛。
func (c *PublicationLeaseCoordinator) BeginDeferred() {
	c.mu.Lock()
	c.deferred = true
	c.mu.Unlock()
}

func (c *PublicationLeaseCoordinator) Create(ctx context.Context, id, publicationID, authorizationScopeHash string, expiresAt, createdAt int64) error {
	if c == nil || c.db == nil {
		return fault.New(fault.CodeInternal, false, fmt.Errorf("publication lease coordinator 未初始化"))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deferred {
		if _, exists := c.pending[id]; !exists && len(c.pending) >= c.maxPendingLease {
			return fault.New(fault.CodeMaintenanceBlocked, true, fmt.Errorf("VACUUM 期间 publication lease 暂存已达到上限"))
		}
		delete(c.pendingDeletes, id)
		c.pending[id] = deferredPublicationLease{
			id:                     id,
			publicationID:          publicationID,
			authorizationScopeHash: authorizationScopeHash,
			expiresAt:              expiresAt,
			createdAt:              createdAt,
		}
		return nil
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO query_publication_leases
(lease_id, query_publication_id, authorization_scope_hash, expires_at, created_at)
VALUES (?, ?, ?, ?, ?)`, id, publicationID, authorizationScopeHash, expiresAt, createdAt); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (c *PublicationLeaseCoordinator) Verify(ctx context.Context, id, publicationID, authorizationScopeHash string, now int64) (bool, error) {
	if c == nil || c.db == nil {
		return false, fmt.Errorf("publication lease coordinator 未初始化")
	}
	c.mu.Lock()
	if pending, ok := c.pending[id]; ok {
		valid := pending.publicationID == publicationID &&
			pending.authorizationScopeHash == authorizationScopeHash && now < pending.expiresAt
		c.mu.Unlock()
		return valid, nil
	}
	c.mu.Unlock()

	var expiresAt int64
	err := c.db.QueryRowContext(ctx, `SELECT expires_at FROM query_publication_leases
WHERE lease_id=? AND query_publication_id=? AND authorization_scope_hash=?`,
		id, publicationID, authorizationScopeHash).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return now < expiresAt, nil
}

func (c *PublicationLeaseCoordinator) Delete(ctx context.Context, id string) error {
	if c == nil || c.db == nil || id == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deferred {
		if _, ok := c.pending[id]; ok {
			delete(c.pending, id)
			return nil
		}
		c.pendingDeletes[id] = struct{}{}
		return nil
	}
	_, err := c.db.ExecContext(ctx, "DELETE FROM query_publication_leases WHERE lease_id=?", id)
	return err
}

// FlushAndEnd 必须在 maintenance.Coordinator 的独占区间内、full VACUUM 返回后调用。
// 整个 flush 持有 mu：新 lease 要么进入本事务，要么在 deferred=false 后直接落盘，
// 不存在“维护锁已释放但 lease 仍只在内存”的切换缝隙。失败时保留全部内存状态，
// 同进程后续 GC 仍会把 pending lease 当作保护事实，下一轮维护可重试 flush。
func (c *PublicationLeaseCoordinator) FlushAndEnd(ctx context.Context) error {
	if c == nil || c.db == nil {
		return fault.New(fault.CodeInternal, false, fmt.Errorf("publication lease coordinator 未初始化"))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.deferred {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	for id := range c.pendingDeletes {
		if _, err := tx.ExecContext(ctx, "DELETE FROM query_publication_leases WHERE lease_id=?", id); err != nil {
			tx.Rollback()
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	for _, lease := range c.pending {
		if _, err := tx.ExecContext(ctx, `INSERT INTO query_publication_leases
(lease_id, query_publication_id, authorization_scope_hash, expires_at, created_at)
VALUES (?, ?, ?, ?, ?)`, lease.id, lease.publicationID, lease.authorizationScopeHash,
			lease.expiresAt, lease.createdAt); err != nil {
			tx.Rollback()
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	clear(c.pending)
	clear(c.pendingDeletes)
	c.deferred = false
	return nil
}

// lockPublicationDeletion 把“检查内存 lease”与 GC 的 publication DELETE 串行化。
// SQL 本身继续核对持久 lease；两者共同关闭 flush 失败后新建内存 lease 与 GC 的竞态。
func (c *PublicationLeaseCoordinator) lockPublicationDeletion(publicationID string, now int64) (protected bool, unlock func()) {
	c.mu.Lock()
	for _, lease := range c.pending {
		if lease.publicationID == publicationID && lease.expiresAt > now {
			return true, c.mu.Unlock
		}
	}
	return false, c.mu.Unlock
}
