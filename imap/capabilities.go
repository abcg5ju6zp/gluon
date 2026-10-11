package imap

type Capability string

const (
	IMAP4rev1 Capability = `IMAP4rev1`
	StartTLS  Capability = `STARTTLS`
	IDLE      Capability = `IDLE`
	UNSELECT  Capability = `UNSELECT`
	UIDPLUS   Capability = `UIDPLUS`
	MOVE      Capability = `MOVE`
	ID        Capability = `ID`
	AUTHPLAIN Capability = `AUTH=PLAIN`
	Enable    Capability = `ENABLE`
	CondStore Capability = `CONDSTORE`
	QResync   Capability = `QRESYNC`
)

func IsCapabilityAvailableBeforeAuth(c Capability) bool {
	switch c {
	case IMAP4rev1, StartTLS, IDLE, ID, AUTHPLAIN:
		return true
	case UNSELECT, UIDPLUS, MOVE, Enable, CondStore, QResync:
		return false
	}

	return false
}
