package mailjet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	netmail "net/mail"
	"slices"
	"strings"

	"github.com/donseba/go-webthings/mail"
)

// Sender delivers mail through Mailjet's Send API v3.1. From must be an
// address on a domain or sender validated in Mailjet.
type Sender struct {
	APIKey    string
	SecretKey string
	From      string
	FromName  string
	// BaseURL replaces DefaultBaseURL, for tests.
	BaseURL string
	// Client replaces the default HTTP client with a 30 second timeout.
	Client *http.Client
}

type address struct {
	Email string `json:"Email"`
	Name  string `json:"Name,omitempty"`
}

type sendMessage struct {
	From     address           `json:"From"`
	To       []address         `json:"To"`
	ReplyTo  *address          `json:"ReplyTo,omitempty"`
	Subject  string            `json:"Subject"`
	TextPart string            `json:"TextPart"`
	HTMLPart string            `json:"HTMLPart,omitempty"`
	Headers  map[string]string `json:"Headers,omitempty"`
}

type sendRequest struct {
	Messages []sendMessage `json:"Messages"`
}

type sendResponse struct {
	Messages []struct {
		Status string `json:"Status"`
	} `json:"Messages"`
}

// Send delivers one message to all its recipients. Message.Headers, such as
// List-Unsubscribe on newsletters, are passed on as Mailjet custom headers.
func (s Sender) Send(ctx context.Context, message mail.Message) error {
	if len(message.To) == 0 {
		return errors.New("mailjet: no recipients")
	}

	if s.From == "" {
		return errors.New("mailjet: no sender address")
	}

	headers, err := checkHeaders(message.Headers)
	if err != nil {
		return err
	}

	request := sendMessage{
		From:     address{Email: s.From, Name: s.FromName},
		Subject:  message.Subject,
		TextPart: message.Text,
		HTMLPart: message.HTML,
		Headers:  headers,
	}

	for _, to := range message.To {
		recipient, err := parse(to)
		if err != nil {
			return err
		}

		request.To = append(request.To, recipient)
	}

	if message.ReplyTo != "" {
		replyTo, err := parse(message.ReplyTo)
		if err != nil {
			return err
		}

		request.ReplyTo = &replyTo
	}

	var response sendResponse

	client := api{key: s.APIKey, secret: s.SecretKey, baseURL: s.BaseURL, client: s.Client}

	err = client.post(ctx, "/v3.1/send", sendRequest{Messages: []sendMessage{request}}, &response)
	if err != nil {
		return err
	}

	for _, sent := range response.Messages {
		if sent.Status != "success" {
			return fmt.Errorf("mailjet: message status %q", sent.Status)
		}
	}

	return nil
}

// reserved are header fields the message sets itself; mail.Compose refuses
// them in Message.Headers as well.
var reserved = []string{
	"bcc", "cc", "content-transfer-encoding", "content-type", "date", "from",
	"mime-version", "reply-to", "sender", "subject", "to",
}

// checkHeaders returns the extra header fields of a message, or an error for
// a name that is not a plain field name or reserved, or a value with a line
// break, so headers cannot be injected. Nil stays nil.
func checkHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}

	for name, value := range headers {
		if !fieldName(name) || slices.Contains(reserved, strings.ToLower(name)) {
			return nil, fmt.Errorf("mailjet: invalid header %q", name)
		}

		if strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("mailjet: header contains a line break")
		}
	}

	return headers, nil
}

// fieldName reports whether name is a header field name: printable ASCII
// without a colon (RFC 5322).
func fieldName(name string) bool {
	if name == "" {
		return false
	}

	for _, r := range name {
		if r < 33 || r > 126 || r == ':' {
			return false
		}
	}

	return true
}

// parse splits "Name <address>" or a plain address.
func parse(value string) (address, error) {
	if strings.ContainsAny(value, "\r\n") {
		return address{}, errors.New("mailjet: address contains a line break")
	}

	parsed, err := netmail.ParseAddress(value)
	if err != nil {
		return address{}, fmt.Errorf("mailjet: address %q: %w", value, err)
	}

	return address{Email: parsed.Address, Name: parsed.Name}, nil
}
