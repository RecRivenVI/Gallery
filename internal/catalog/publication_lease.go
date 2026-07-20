package catalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

// PublicationReadLeaseDuration 覆盖一次 HTTP 请求解析 + 读取快照 publication 的时长；
// 只在客户端显式指定 queryPublicationId（snapshot 模式）时才需要获取，current 模式下
// active publication 本身永不被 GC 回收，不需要额外租约。
const PublicationReadLeaseDuration = 2 * time.Minute

// PublicationReadLease 与 media.BlobReadLease 同构：复用既有 query_publication_leases
// 表与 GC 保护判据（GarbageCollect 对任一未过期 lease 的 publication 一律跳过回收），
// 不为按需读取引入第二套 lease 表或保护语义。
type PublicationReadLease struct {
	store  *Store
	id     string
	closed sync.Once
}

// AcquirePublicationLease 为显式快照读取建立一个短期 lease，防止在本次请求完成前
// GarbageCollect 回收该 publication。publicationID 必须已通过 resolvePublication 确认
// 存在；调用方在读取完成后必须 Close 释放（defer 语义），未及时 Close 也会在
// PublicationReadLeaseDuration 后自然过期，不会永久占用。
func (s *Store) AcquirePublicationLease(ctx context.Context, publicationID, authorizationScopeHash string) (*PublicationReadLease, error) {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	id := "qlease_" + hex.EncodeToString(buffer)
	now := s.clock.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO query_publication_leases
(lease_id, query_publication_id, authorization_scope_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, publicationID, authorizationScopeHash, now.Add(PublicationReadLeaseDuration).Unix(), now.Unix()); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return &PublicationReadLease{store: s, id: id}, nil
}

func (l *PublicationReadLease) Close() error {
	if l == nil {
		return nil
	}
	var result error
	l.closed.Do(func() {
		if l.store != nil && l.id != "" {
			_, result = l.store.db.Exec("DELETE FROM query_publication_leases WHERE lease_id=?", l.id)
		}
	})
	return result
}
