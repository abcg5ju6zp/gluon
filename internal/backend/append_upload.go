package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/internal/spool"
	"github.com/ProtonMail/gluon/internal/state"
	"github.com/ProtonMail/gluon/rfcparser"
	"github.com/sirupsen/logrus"
)

type appendUpload struct {
	user *user
	obj  *spool.Object

	reservationID string

	materialized []byte
	once         sync.Once
	abortOnce    sync.Once
}

// beginAppendUpload reserves quota, creates the spool object and records a receiving journal
// row. No connector call happens at this point.
func (u *user) beginAppendUpload(ctx context.Context, declared int64) (state.Upload, error) {
	if declared <= 0 || declared >= int64(rfcparser.MaxLiteralSize) {
		return nil, fmt.Errorf("%w: declared size %d", state.ErrAppendSpoolFailure, declared)
	}

	reservationID, err := u.quota.Reserve(ctx, declared)
	if err != nil {
		if errors.Is(err, ErrAppendQuotaExceeded) {
			return nil, fmt.Errorf("%w: %v", state.ErrAppendQuotaExceeded, err)
		}

		return nil, err
	}

	releaseOnError := true

	defer func() {
		if releaseOnError {
			u.quota.Release(reservationID)
		}
	}()

	obj, err := u.spool.Begin(declared)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", state.ErrAppendSpoolFailure, err)
	}

	removeOnError := true

	defer func() {
		if removeOnError {
			_ = obj.Remove()
		}
	}()

	if err := u.db.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		return tx.InsertAppendJournalReceiving(ctx, obj.AttemptID(), declared, time.Now())
	}); err != nil {
		return nil, fmt.Errorf("failed to record append reception: %w", err)
	}

	removeOnError = false
	releaseOnError = false

	return &appendUpload{user: u, obj: obj, reservationID: reservationID}, nil
}

func (u *appendUpload) AttemptID() string {
	return u.obj.AttemptID()
}

func (u *appendUpload) DeclaredSize() int64 {
	return u.obj.Declared()
}

func (u *appendUpload) ContentHash() string {
	return u.obj.Hash()
}

func (u *appendUpload) OnDisk() bool {
	return u.obj.OnDisk()
}

func (u *appendUpload) Open() (io.ReadSeekCloser, error) {
	return u.obj.Open()
}

func (u *appendUpload) Write(p []byte) (int, error) {
	return u.obj.Write(p)
}

func (u *appendUpload) CloseReception() error {
	return u.obj.Close()
}

func (u *appendUpload) Materialize(_ context.Context) ([]byte, error) {
	var err error

	u.once.Do(func() {
		u.materialized, err = u.obj.Bytes()
	})

	return u.materialized, err
}

func (u *appendUpload) ReleaseReservation() {
	u.user.quota.Release(u.reservationID)
	u.reservationID = ""
}

func (u *appendUpload) RemoveSpool() error {
	return u.obj.Remove()
}

func (u *appendUpload) Abort(ctx context.Context) {
	u.abortOnce.Do(func() {
		u.ReleaseReservation()

		if err := u.user.db.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
			return tx.DeleteAppendJournal(ctx, u.obj.AttemptID())
		}); err != nil {
			logrus.WithError(err).
				WithField("attempt_id", u.obj.AttemptID()).
				Error("Failed to delete append journal entry on abort")
		}

		if err := u.obj.Remove(); err != nil {
			logrus.WithError(err).
				WithField("attempt_id", u.obj.AttemptID()).
				Error("Failed to remove append spool object on abort")
		}
	})
}
