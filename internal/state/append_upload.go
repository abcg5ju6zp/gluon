package state

import (
	"context"
	"errors"
	"io"
)

// Diagnostic sentinels for the streaming APPEND flow.
var (
	// ErrAppendQuotaExceeded is returned before reception when the user quota cannot be
	// reserved. No message side effects occur and the client may retry.
	ErrAppendQuotaExceeded = errors.New("append rejected: quota exceeded")

	// ErrAppendSpoolFailure is returned when the temporary reception object cannot be
	// created or written. The connector was never called and the client may retry.
	ErrAppendSpoolFailure = errors.New("append rejected: failed to spool literal")

	// ErrAppendAcceptedButNotLocallyConfirmed is returned when the connector accepted the
	// message but the local store/DB commit could not be completed. The message must not be
	// created again; a repeated identical APPEND recovers it by completing the local commit.
	ErrAppendAcceptedButNotLocallyConfirmed = errors.New("append accepted remotely but not locally confirmed")
)

// Upload is a received APPEND literal streamed into a temporary object, with its quota
// reservation and append journal row. Ownership moves from the command reader to the commit
// handler exactly once.
type Upload interface {
	// AttemptID is the unique ID of this reception attempt (journal row / spool file name).
	AttemptID() string

	// DeclaredSize is the literal size declared by the client.
	DeclaredSize() int64

	// ContentHash is the hex SHA-256 of the received bytes.
	ContentHash() string

	// OnDisk reports whether the spool object spilled to a temporary file.
	OnDisk() bool

	// Open returns a seekable reader over the received bytes.
	Open() (io.ReadSeekCloser, error)

	// Write streams received bytes into the backing spool object.
	Write(p []byte) (int, error)

	// CloseReception seals the spool object once the whole body has been received.
	CloseReception() error

	// Materialize returns the received bytes, caching them for repeated reads.
	Materialize(ctx context.Context) ([]byte, error)

	// ReleaseReservation frees this upload's quota reservation. Idempotent.
	ReleaseReservation()

	// RemoveSpool deletes the temporary object but keeps the journal row. Idempotent.
	RemoveSpool() error

	// Abort undoes the whole reception: releases the reservation, deletes the journal row
	// and removes the spool object. Idempotent.
	Abort(ctx context.Context)
}
