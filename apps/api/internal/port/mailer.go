package port

import "context"

// Mailer is the outbound-email port (P4-06). The weekly report scheduler drives
// it; a fake stands in for tests and local dev (no SMTP server needed).
type Mailer interface {
	// Send delivers one message. Recipients are plain addresses; the adapter
	// formats them for the transport. An attachment is optional (nil = none)
	// and is sent as a base64 part with the given filename and MIME type.
	Send(ctx context.Context, msg Email) error
}

// Email is one outbound message.
type Email struct {
	To         []string
	Subject    string
	Body       string // plain text
	HTML       string // optional; empty means send plain text only
	Attachment []byte // optional
	AttachName string
	AttachMIME string
}
