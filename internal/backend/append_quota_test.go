package backend

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ProtonMail/gluon/connector"
	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/stretchr/testify/require"
)

type quotaReadOnly struct {
	db.ReadOnly
	total int64
}

func (r quotaReadOnly) GetTotalMessageSize(_ context.Context) (int64, error) {
	return r.total, nil
}

type fakeQuotaClient struct {
	mu    sync.RWMutex
	total int64
}

func (c *fakeQuotaClient) Init(_ context.Context, _ imap.UIDValidityGenerator) error { return nil }

func (c *fakeQuotaClient) Read(ctx context.Context, op func(context.Context, db.ReadOnly) error) error {
	c.mu.RLock()
	ro := quotaReadOnly{total: c.total}
	c.mu.RUnlock()

	return op(ctx, ro)
}

func (c *fakeQuotaClient) Write(ctx context.Context, op func(context.Context, db.Transaction) error) error {
	return op(ctx, nil)
}

func (c *fakeQuotaClient) Close() error { return nil }

func newQuotaTestConnector(t *testing.T, limit int64) *connector.Dummy {
	t.Helper()

	d := connector.NewDummy([]string{"user"}, []byte("pass"), time.Hour, imap.NewFlagSet(), imap.NewFlagSet(), imap.NewFlagSet())
	d.SetQuotaBytes(limit)
	t.Cleanup(func() { _ = d.Close(context.Background()) })

	return d
}

func TestAppendQuotaManager_AllowsUnlimited(t *testing.T) {
	database := &fakeQuotaClient{}
	mgr := newAppendQuotaManager(database, newQuotaTestConnector(t, 0))

	id, err := mgr.Reserve(context.Background(), 1<<40)
	require.NoError(t, err)
	mgr.Release(id)
}

func TestAppendQuotaManager_RejectsWhenFull(t *testing.T) {
	database := &fakeQuotaClient{total: 90}
	mgr := newAppendQuotaManager(database, newQuotaTestConnector(t, 100))

	_, err := mgr.Reserve(context.Background(), 20)
	require.ErrorIs(t, err, ErrAppendQuotaExceeded)

	id, err := mgr.Reserve(context.Background(), 10)
	require.NoError(t, err)
	mgr.Release(id)

	// Releasing an unknown reservation is a no-op.
	mgr.Release("does-not-exist")
}

func TestAppendQuotaManager_ConcurrentReservationsNeverExceedLimit(t *testing.T) {
	const (
		goroutines = 50
		size       = 10
		limit      = 150
	)

	database := &fakeQuotaClient{total: 0}
	mgr := newAppendQuotaManager(database, newQuotaTestConnector(t, limit))

	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	reserved := make(chan string, goroutines)

	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			id, err := mgr.Reserve(context.Background(), size)
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
				reserved <- id
			}
		}()
	}

	close(start)
	wg.Wait()
	close(reserved)

	require.Equal(t, limit/size, success)

	// Every successful reservation is released at the end.
	for id := range reserved {
		mgr.Release(id)
	}
}

func TestAppendQuotaManager_ReleaseFreesCapacity(t *testing.T) {
	database := &fakeQuotaClient{total: 0}
	mgr := newAppendQuotaManager(database, newQuotaTestConnector(t, 10))

	id1, err := mgr.Reserve(context.Background(), 10)
	require.NoError(t, err)

	_, err = mgr.Reserve(context.Background(), 1)
	require.ErrorIs(t, err, ErrAppendQuotaExceeded)

	mgr.Release(id1)

	id2, err := mgr.Reserve(context.Background(), 10)
	require.NoError(t, err)
	mgr.Release(id2)
}

func TestAppendQuotaManager_ReservationsAreIndependent(t *testing.T) {
	database := &fakeQuotaClient{total: 0}
	mgr := newAppendQuotaManager(database, newQuotaTestConnector(t, 20))

	id1, err := mgr.Reserve(context.Background(), 10)
	require.NoError(t, err)

	id2, err := mgr.Reserve(context.Background(), 10)
	require.NoError(t, err)

	// Releasing one reservation must not free the other's quota.
	mgr.Release(id1)

	_, err = mgr.Reserve(context.Background(), 10)
	require.NoError(t, err, "released capacity available again")

	_, err = mgr.Reserve(context.Background(), 1)
	require.ErrorIs(t, err, ErrAppendQuotaExceeded, "id2 reservation still counts")

	mgr.Release(id2)
}
