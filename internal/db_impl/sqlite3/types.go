package sqlite3

import (
	"database/sql"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/db_impl/sqlite3/utils"
)

// nullableObjectID scans a nullable persistent object id column into a string-backed strong type.
func scanMailboxObjectID(value sql.NullString) imap.MailboxObjectID {
	if !value.Valid {
		return ""
	}

	return imap.MailboxObjectID(value.String)
}

func scanEmailID(value sql.NullString) imap.EmailID {
	if !value.Valid {
		return ""
	}

	return imap.EmailID(value.String)
}

func scanThreadID(value sql.NullString) imap.ThreadID {
	if !value.Valid {
		return ""
	}

	return imap.ThreadID(value.String)
}

func ScanMailbox(scanner utils.RowScanner) (*db.Mailbox, error) {
	mbox := new(db.Mailbox)

	var objectID sql.NullString

	if err := scanner.Scan(
		&mbox.ID, &mbox.RemoteID, &mbox.Name, &mbox.UIDValidity, &mbox.Subscribed, &objectID,
	); err != nil {
		return nil, err
	}

	mbox.ObjectID = scanMailboxObjectID(objectID)

	return mbox, nil
}

func ScanMailboxWithAttr(scanner utils.RowScanner) (*db.MailboxWithAttr, error) {
	mbox := new(db.MailboxWithAttr)

	var objectID sql.NullString

	if err := scanner.Scan(
		&mbox.ID, &mbox.RemoteID, &mbox.Name, &mbox.UIDValidity, &mbox.Subscribed, &objectID,
	); err != nil {
		return nil, err
	}

	mbox.ObjectID = scanMailboxObjectID(objectID)

	return mbox, nil
}

func scanMessageRow(scanner utils.RowScanner, msg *db.Message) error {
	var (
		objectID sql.NullString
		threadID sql.NullString
	)

	if err := scanner.Scan(
		&msg.ID, &msg.RemoteID, &msg.Date, &msg.Size, &msg.Body, &msg.BodyStructure,
		&msg.Envelope, &msg.Deleted, &objectID, &threadID,
	); err != nil {
		return err
	}

	msg.ObjectID = scanEmailID(objectID)
	msg.ThreadID = scanThreadID(threadID)

	return nil
}

func ScanMessage(scanner utils.RowScanner) (*db.Message, error) {
	msg := new(db.Message)

	if err := scanMessageRow(scanner, msg); err != nil {
		return nil, err
	}

	return msg, nil
}

func ScanMessageWithFlags(scanner utils.RowScanner) (*db.MessageWithFlags, error) {
	msg := new(db.MessageWithFlags)

	if err := scanMessageRow(scanner, &msg.Message); err != nil {
		return nil, err
	}

	return msg, nil
}
