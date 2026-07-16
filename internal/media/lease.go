package media

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"io"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
)

const BlobReadLeaseDuration = 5 * time.Minute

type BlobReadLease struct {
	db     *sql.DB
	id     string
	closed sync.Once
}

func AcquireBlobReadLease(ctx context.Context, db *sql.DB, clock ports.Clock, blob domain.ContentBlobRef, random io.Reader) (*BlobReadLease, error) {
	if db == nil || clock == nil {
		return nil, fault.New(fault.CodeInternal, false, nil)
	}
	if _, err := domain.ParseContentBlobRef(blob.Algorithm, blob.Digest); err != nil {
		return nil, fault.New(fault.CodeValidation, false, err)
	}
	if random == nil {
		random = rand.Reader
	}
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	id := "blease_" + hex.EncodeToString(buffer)
	now := clock.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO blob_read_leases
(lease_id, blob_algorithm, blob_digest, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, blob.Algorithm, blob.Digest, now.Add(BlobReadLeaseDuration).Unix(), now.Unix()); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return &BlobReadLease{db: db, id: id}, nil
}

func (l *BlobReadLease) Close() error {
	if l == nil {
		return nil
	}
	var result error
	l.closed.Do(func() {
		if l.db != nil && l.id != "" {
			_, result = l.db.Exec("DELETE FROM blob_read_leases WHERE lease_id=?", l.id)
		}
	})
	return result
}
