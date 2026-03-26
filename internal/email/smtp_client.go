package email

import (
	"bytes"
	"fmt"
	"log"
	"net"
	gosmtp "net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SMTPClient handles outbound email delivery.
type SMTPClient struct {
	store  *Store
	domain string
}

func NewSMTPClient(store *Store, domain string) *SMTPClient {
	return &SMTPClient{store: store, domain: domain}
}

// Send composes and queues an outbound email message.
func (c *SMTPClient) Send(from string, to []string, cc []string, subject, bodyText, bodyHTML string) (*Message, error) {
	if from == "" {
		from = "noreply@" + c.domain
	}

	id := newID("msg_")
	msgID := fmt.Sprintf("<%s@%s>", id, c.domain)
	now := time.Now().UTC()

	// Build raw message
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(to, ", "))
	if len(cc) > 0 {
		fmt.Fprintf(&buf, "Cc: %s\r\n", strings.Join(cc, ", "))
	}
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "Message-Id: %s\r\n", msgID)
	fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")

	if bodyHTML != "" {
		boundary := fmt.Sprintf("boundary_%s", id)
		fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%s\r\n", boundary)
		fmt.Fprintf(&buf, "\r\n")
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n\r\n")
		fmt.Fprintf(&buf, "%s\r\n", bodyText)
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: text/html; charset=utf-8\r\n\r\n")
		fmt.Fprintf(&buf, "%s\r\n", bodyHTML)
		fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	} else {
		fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
		fmt.Fprintf(&buf, "\r\n")
		fmt.Fprintf(&buf, "%s\r\n", bodyText)
	}

	rawData := buf.Bytes()

	// Save raw .eml
	rawPath := filepath.Join(c.store.rawDir, id+".eml")
	if err := os.WriteFile(rawPath, rawData, 0640); err != nil {
		return nil, fmt.Errorf("saving raw outbound message: %w", err)
	}

	allRecipients := append(to, cc...)

	msg := &Message{
		ID:        id,
		MessageID: msgID,
		ThreadID:  cleanMessageID(msgID),
		From:      from,
		To:        to,
		Cc:        cc,
		Subject:   subject,
		Date:      now.Format(time.RFC3339),
		BodyText:  bodyText,
		BodyHTML:  bodyHTML,
		RawPath:   rawPath,
		SizeBytes: int64(len(rawData)),
		Direction: "outbound",
		Status:    "queued",
	}

	if err := c.store.SaveMessage(msg); err != nil {
		return nil, fmt.Errorf("saving outbound message: %w", err)
	}

	// Add sent label
	c.store.AddLabel(id, "sent")

	// Enqueue for delivery
	if err := c.store.EnqueueOutbound(id, allRecipients); err != nil {
		return nil, fmt.Errorf("enqueueing delivery: %w", err)
	}

	return msg, nil
}

// ProcessQueue attempts to deliver queued messages.
func (c *SMTPClient) ProcessQueue() {
	entries, err := c.store.GetPendingQueue(10)
	if err != nil {
		log.Printf("Error getting queue: %v", err)
		return
	}

	for _, entry := range entries {
		err := c.deliverToRecipient(entry)
		if err != nil {
			log.Printf("Delivery failed for %s to %s: %v", entry.MessageID, entry.Recipient, err)
			// Retry with backoff
			attempts := entry.Attempts + 1
			if attempts >= 5 {
				c.store.UpdateQueueEntry(entry.ID, "failed", err.Error(), "")
				c.store.db.Exec("UPDATE messages SET status = 'failed' WHERE id = ?", entry.MessageID)
			} else {
				backoff := time.Duration(attempts*attempts) * time.Minute
				nextRetry := time.Now().Add(backoff).UTC().Format(time.RFC3339)
				c.store.UpdateQueueEntry(entry.ID, "queued", err.Error(), nextRetry)
			}
		} else {
			c.store.UpdateQueueEntry(entry.ID, "sent", "", "")
			c.store.db.Exec("UPDATE messages SET status = 'sent' WHERE id = ?", entry.MessageID)
			log.Printf("Delivered %s to %s", entry.MessageID, entry.Recipient)
		}
	}
}

func (c *SMTPClient) deliverToRecipient(entry QueueEntry) error {
	// Read raw message
	msg, err := c.store.Get(entry.MessageID)
	if err != nil || msg == nil {
		return fmt.Errorf("message not found: %s", entry.MessageID)
	}

	rawData, err := os.ReadFile(msg.RawPath)
	if err != nil {
		return fmt.Errorf("reading raw message: %w", err)
	}

	// Get recipient domain and resolve MX
	parts := strings.SplitN(entry.Recipient, "@", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid recipient: %s", entry.Recipient)
	}
	domain := parts[1]

	mxRecords, err := net.LookupMX(domain)
	if err != nil || len(mxRecords) == 0 {
		return fmt.Errorf("MX lookup failed for %s: %w", domain, err)
	}

	// Try each MX host in priority order
	var lastErr error
	for _, mx := range mxRecords {
		host := strings.TrimSuffix(mx.Host, ".")
		addr := host + ":25"

		err := gosmtp.SendMail(addr, nil, msg.From, []string{entry.Recipient}, rawData)
		if err == nil {
			return nil
		}
		lastErr = err
		log.Printf("MX %s failed: %v, trying next...", host, err)
	}

	return fmt.Errorf("all MX hosts failed, last error: %w", lastErr)
}

// StartQueueProcessor runs the delivery queue processor in the background.
func (c *SMTPClient) StartQueueProcessor(stop chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Process immediately on start
	c.ProcessQueue()

	for {
		select {
		case <-ticker.C:
			c.ProcessQueue()
		case <-stop:
			return
		}
	}
}
