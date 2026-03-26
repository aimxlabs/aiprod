package email

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-smtp"
)

// SMTPServer handles inbound email via SMTP protocol.
type SMTPServer struct {
	server  *smtp.Server
	store   *Store
	domain  string
}

func NewSMTPServer(store *Store, domain, addr string) *SMTPServer {
	s := &SMTPServer{store: store, domain: domain}

	backend := &smtpBackend{svc: s}
	server := smtp.NewServer(backend)
	server.Addr = addr
	server.Domain = domain
	server.ReadTimeout = 30 * time.Second
	server.WriteTimeout = 30 * time.Second
	server.MaxMessageBytes = 25 * 1024 * 1024 // 25MB
	server.MaxRecipients = 50
	server.AllowInsecureAuth = true // For local dev; TLS should be enforced in production

	s.server = server
	return s
}

func (s *SMTPServer) ListenAndServe() error {
	log.Printf("SMTP server listening on %s for domain %s", s.server.Addr, s.domain)
	return s.server.ListenAndServe()
}

func (s *SMTPServer) Close() error {
	return s.server.Close()
}

// smtpBackend implements smtp.Backend
type smtpBackend struct {
	svc *SMTPServer
}

func (b *smtpBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &smtpSession{svc: b.svc}, nil
}

// smtpSession implements smtp.Session
type smtpSession struct {
	svc  *SMTPServer
	from string
	to   []string
}

func (s *smtpSession) AuthPlain(username, password string) error {
	return nil // Accept all for now; add auth later
}

func (s *smtpSession) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *smtpSession) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *smtpSession) Data(r io.Reader) error {
	// Read full message
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading message data: %w", err)
	}

	// Parse the message
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parsing message: %w", err)
	}

	// Generate internal ID
	id := newID("msg_")

	// Save raw .eml
	rawPath := filepath.Join(s.svc.store.rawDir, id+".eml")
	if err := os.WriteFile(rawPath, data, 0640); err != nil {
		return fmt.Errorf("saving raw message: %w", err)
	}

	// Extract headers
	messageID := msg.Header.Get("Message-Id")
	subject := msg.Header.Get("Subject")
	from := msg.Header.Get("From")
	date := msg.Header.Get("Date")
	if date == "" {
		date = time.Now().UTC().Format(time.RFC1123Z)
	}

	// Parse To/Cc addresses
	toAddrs := parseAddressList(msg.Header.Get("To"))
	ccAddrs := parseAddressList(msg.Header.Get("Cc"))

	// Compute thread ID from References/In-Reply-To
	threadID := computeThreadID(msg.Header)

	// Extract body text
	bodyText, bodyHTML := extractBody(msg)

	// Save to database
	m := &Message{
		ID:        id,
		MessageID: messageID,
		ThreadID:  threadID,
		From:      from,
		To:        toAddrs,
		Cc:        ccAddrs,
		Subject:   subject,
		Date:      date,
		BodyText:  bodyText,
		BodyHTML:  bodyHTML,
		RawPath:   rawPath,
		SizeBytes: int64(len(data)),
		Direction: "inbound",
		Status:    "received",
	}

	if err := s.svc.store.SaveMessage(m); err != nil {
		return fmt.Errorf("saving message to store: %w", err)
	}

	// Add inbox label
	s.svc.store.AddLabel(id, "inbox")

	log.Printf("Received message %s from %s, subject: %s", id, from, subject)
	return nil
}

func (s *smtpSession) Reset() {
	s.from = ""
	s.to = nil
}

func (s *smtpSession) Logout() error {
	return nil
}

// parseAddressList extracts email addresses from a header value.
func parseAddressList(header string) []string {
	if header == "" {
		return []string{}
	}
	addrs, err := mail.ParseAddressList(header)
	if err != nil {
		// Fallback: split by comma
		parts := strings.Split(header, ",")
		var result []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				result = append(result, p)
			}
		}
		return result
	}
	var result []string
	for _, a := range addrs {
		result = append(result, a.Address)
	}
	return result
}

// computeThreadID generates a thread identifier from References and In-Reply-To headers.
// This is a simplified version of the JWZ threading algorithm.
func computeThreadID(headers mail.Header) string {
	// Use the first message-id from References, or In-Reply-To, or generate from Message-Id
	refs := headers.Get("References")
	if refs != "" {
		parts := strings.Fields(refs)
		if len(parts) > 0 {
			return cleanMessageID(parts[0])
		}
	}
	inReplyTo := headers.Get("In-Reply-To")
	if inReplyTo != "" {
		return cleanMessageID(inReplyTo)
	}
	// New thread: use own message ID as thread ID
	msgID := headers.Get("Message-Id")
	if msgID != "" {
		return cleanMessageID(msgID)
	}
	return newID("thread_")
}

func cleanMessageID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return id
}

// extractBody reads the message body, handling multipart MIME.
func extractBody(msg *mail.Message) (text, html string) {
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		body, _ := io.ReadAll(msg.Body)
		return string(body), ""
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(msg.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			partCT := part.Header.Get("Content-Type")
			partData, _ := io.ReadAll(part)

			if strings.HasPrefix(partCT, "text/plain") {
				text = string(partData)
			} else if strings.HasPrefix(partCT, "text/html") {
				html = string(partData)
			}
		}
		return text, html
	}

	body, _ := io.ReadAll(msg.Body)
	if strings.HasPrefix(mediaType, "text/html") {
		return "", string(body)
	}
	return string(body), ""
}
