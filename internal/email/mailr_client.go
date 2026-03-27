package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// Sender is the interface for outbound email. Both SMTPClient and MailrClient implement it.
type Sender interface {
	Send(from string, to []string, cc []string, subject, bodyText, bodyHTML string) (*Message, error)
}

// MailrClient sends and receives email through a remote mailr relay server.
type MailrClient struct {
	store     *Store
	client    *http.Client
	baseURL   string
	domainID  string
	authToken string
}

func NewMailrClient(store *Store, baseURL, domainID, authToken string) *MailrClient {
	return &MailrClient{
		store:     store,
		client:    &http.Client{Timeout: 30 * time.Second},
		baseURL:   baseURL,
		domainID:  domainID,
		authToken: authToken,
	}
}

// Send submits an outbound email to mailr for delivery.
func (c *MailrClient) Send(from string, to []string, cc []string, subject, bodyText, bodyHTML string) (*Message, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"from":      from,
		"to":        to,
		"cc":        cc,
		"subject":   subject,
		"body_text": bodyText,
		"body_html": bodyHTML,
	})

	req, err := http.NewRequest("POST", c.apiURL("/send"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mailr send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mailr send returned %d: %s", resp.StatusCode, string(respBody))
	}

	var mailrMsg struct {
		ID      string `json:"id"`
		From    string `json:"from"`
		Subject string `json:"subject"`
		Status  string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&mailrMsg)

	// Store locally in aiprod's email.db
	id := newID("msg_")
	now := time.Now().UTC().Format(time.RFC3339)
	msg := &Message{
		ID:        id,
		MessageID: fmt.Sprintf("<%s@mailr>", mailrMsg.ID),
		ThreadID:  mailrMsg.ID,
		From:      from,
		To:        to,
		Cc:        cc,
		Subject:   subject,
		Date:      now,
		BodyText:  bodyText,
		BodyHTML:  bodyHTML,
		Direction: "outbound",
		Status:    "queued",
	}
	c.store.SaveMessage(msg)
	c.store.AddLabel(id, "sent")

	return msg, nil
}

// RegisterAddress creates an address on the mailr server.
func (c *MailrClient) RegisterAddress(localPart, label string) error {
	body, _ := json.Marshal(map[string]string{
		"local_part": localPart,
		"label":      label,
	})

	req, _ := http.NewRequest("POST", c.apiURL("/addresses"), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("mailr register address: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mailr register address returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// PollInbound fetches new inbound messages from mailr and stores them locally.
func (c *MailrClient) PollInbound() (int, error) {
	req, _ := http.NewRequest("GET", c.apiURL("/messages/poll?limit=100"), nil)
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("mailr poll: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("mailr poll returned %d", resp.StatusCode)
	}

	var pollResp struct {
		Messages []struct {
			ID       string   `json:"id"`
			From     string   `json:"from"`
			To       []string `json:"to"`
			Cc       []string `json:"cc"`
			Subject  string   `json:"subject"`
			BodyText string   `json:"body_text"`
			BodyHTML string   `json:"body_html"`
		} `json:"messages"`
	}
	json.NewDecoder(resp.Body).Decode(&pollResp)

	if len(pollResp.Messages) == 0 {
		return 0, nil
	}

	var ackIDs []string
	for _, m := range pollResp.Messages {
		id := newID("msg_")
		now := time.Now().UTC().Format(time.RFC3339)
		msg := &Message{
			ID:        id,
			MessageID: fmt.Sprintf("<%s@mailr>", m.ID),
			ThreadID:  m.ID,
			From:      m.From,
			To:        m.To,
			Cc:        m.Cc,
			Subject:   m.Subject,
			Date:      now,
			BodyText:  m.BodyText,
			BodyHTML:  m.BodyHTML,
			Direction: "inbound",
			Status:    "received",
		}
		if err := c.store.SaveMessage(msg); err != nil {
			log.Printf("email: failed to store polled message: %v", err)
			continue
		}
		c.store.AddLabel(id, "inbox")
		ackIDs = append(ackIDs, m.ID)
	}

	// ACK received messages
	if len(ackIDs) > 0 {
		c.ackMessages(ackIDs)
	}

	return len(ackIDs), nil
}

func (c *MailrClient) ackMessages(ids []string) {
	body, _ := json.Marshal(map[string]interface{}{"message_ids": ids})
	req, _ := http.NewRequest("POST", c.apiURL("/messages/ack"), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken)
	c.client.Do(req)
}

// StartPollProcessor polls mailr for inbound messages on a timer.
func (c *MailrClient) StartPollProcessor(stop chan struct{}) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Poll immediately on start
	if n, err := c.PollInbound(); err != nil {
		log.Printf("email: mailr poll error: %v", err)
	} else if n > 0 {
		log.Printf("email: polled %d messages from mailr", n)
	}

	for {
		select {
		case <-ticker.C:
			if n, err := c.PollInbound(); err != nil {
				log.Printf("email: mailr poll error: %v", err)
			} else if n > 0 {
				log.Printf("email: polled %d messages from mailr", n)
			}
		case <-stop:
			return
		}
	}
}

func (c *MailrClient) apiURL(path string) string {
	return c.baseURL + "/api/domains/" + c.domainID + path
}
