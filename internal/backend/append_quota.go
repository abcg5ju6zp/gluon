package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ProtonMail/gluon/connector"
	"github.com/ProtonMail/gluon/db"
	"github.com/google/uuid"
)

// ErrAppendQuotaExceeded indicates an APPEND was rejected before reception because the user's
// storage quota could not be reserved. Nothing has been created when this error is returned.
var ErrAppendQuotaExceeded = errors.New("append quota exceeded")

// appendQuotaManager hands out size reservations for in-flight APPEND uploads. The aggregate
// of committed message sizes and outstanding reservations never exceeds the connector-provided
// quota. Reservations are keyed by ID so releasing one upload can never free another one.
type appendQuotaManager struct {
	mu        sync.Mutex
	database  db.Client
	connector connector.Connector
	reserved  map[string]int64
}

func newAppendQuotaManager(database db.Client, conn connector.Connector) *appendQuotaManager {
	return &appendQuotaManager{
		database:  database,
		connector: conn,
		reserved:  make(map[string]int64),
	}
}

// Reserve reserves size bytes for a new upload. The returned ID must be passed to Release
// once the upload commits or aborts.
func (m *appendQuotaManager) Reserve(ctx context.Context, size int64) (string, error) {
	if size <= 0 {
		return "", fmt.Errorf("cannot reserve non-positive size %d", size)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	limit := int64(0)

	if quotaProvider, ok := m.connector.(connector.QuotaConnector); ok {
		var err error

		limit, err = quotaProvider.GetQuotaBytes(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to determine append quota: %w", err)
		}
	}

	if limit > 0 {
		used, err := db.ClientReadType(ctx, m.database, func(ctx context.Context, c db.ReadOnly) (int64, error) {
			return c.GetTotalMessageSize(ctx)
		})
		if err != nil {
			return "", fmt.Errorf("failed to read used quota: %w", err)
		}

		var reservedTotal int64
		for _, reserved := range m.reserved {
			reservedTotal += reserved
		}

		if used+reservedTotal+size > limit {
			return "", fmt.Errorf("%w: used=%d reserved=%d requested=%d limit=%d",
				ErrAppendQuotaExceeded, used, reservedTotal, size, limit)
		}
	}

	id := uuid.NewString()
	m.reserved[id] = size

	return id, nil
}

// Release frees the reservation with the given ID. It is a no-op for unknown IDs, so it is
// safe to call for an upload that never reserved anything.
func (m *appendQuotaManager) Release(reservationID string) {
	if reservationID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.reserved, reservationID)
}
