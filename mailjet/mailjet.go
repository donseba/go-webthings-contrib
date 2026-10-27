// Package mailjet connects a site to Mailjet over its HTTP API, without an SDK.
//
// Sender delivers the site's mail (webthings.Config.Mail) through the Send API
// v3.1:
//
//	app := webthings.New(webthings.Config{
//		Mail: mailjet.Sender{APIKey: key, SecretKey: secret, From: "news@example.com", FromName: "Example"},
//	})
//
// List keeps a Mailjet contact list in step with a newsletter instance: it
// adds confirmed subscribers and unsubscribes those who leave.
//
//	app.Use(newsletter.Module("news", "Newsletter", newsletter.WithProvider(mailjet.List{
//		APIKey: key, SecretKey: secret, ListID: 12345,
//	})))
//
// BaseURL replaces https://api.mailjet.com, for tests.
package mailjet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is Mailjet's API address.
const DefaultBaseURL = "https://api.mailjet.com"

// defaultClient is used when no Client is set; a timeout keeps a slow API
// from holding up a visitor's request.
var defaultClient = &http.Client{Timeout: 30 * time.Second}

// Error is an answer from Mailjet that is not a success.
type Error struct {
	// Status is the HTTP status code.
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("mailjet: %d %s", e.Status, e.Message)
}

// api is what Sender and List share: credentials, address and HTTP client.
type api struct {
	key     string
	secret  string
	baseURL string
	client  *http.Client
}

// post sends body as JSON to path with basic authentication and decodes a
// successful answer into out. Other answers become an *Error.
func (a api) post(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	base := a.baseURL
	if base == "" {
		base = DefaultBaseURL
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(base, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	request.SetBasicAuth(a.key, a.secret)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	client := a.client
	if client == nil {
		client = defaultClient
	}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("mailjet: %w", err)
	}

	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("mailjet: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return &Error{Status: response.StatusCode, Message: message(data)}
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("mailjet: answer: %w", err)
	}

	return nil
}

// message extracts the error messages from an error answer; Mailjet uses
// ErrorMessage at the top level, or per message under Messages[].Errors.
func message(data []byte) string {
	var answer struct {
		ErrorMessage string
		Messages     []struct {
			Errors []struct {
				ErrorMessage string
			}
		}
	}

	if err := json.Unmarshal(data, &answer); err != nil {
		return strings.TrimSpace(string(data))
	}

	messages := []string{}
	if answer.ErrorMessage != "" {
		messages = append(messages, answer.ErrorMessage)
	}

	for _, sent := range answer.Messages {
		for _, problem := range sent.Errors {
			messages = append(messages, problem.ErrorMessage)
		}
	}

	if len(messages) == 0 {
		return strings.TrimSpace(string(data))
	}

	return strings.Join(messages, "; ")
}
