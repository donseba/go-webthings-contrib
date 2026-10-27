package mailjet

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

// List is a Mailjet contact list that follows a newsletter: it implements
// newsletter.Provider. Confirmed subscribers are added (or re-subscribed), and
// subscribers who leave are unsubscribed from the list, not deleted, so
// Mailjet keeps their history.
type List struct {
	APIKey    string
	SecretKey string
	ListID    int64
	// LocaleProperty names a contact property in Mailjet that receives the
	// subscriber's language, for example "language". Empty sends none; the
	// property must exist in Mailjet.
	LocaleProperty string
	// BaseURL replaces DefaultBaseURL, for tests.
	BaseURL string
	// Client replaces the default HTTP client with a 30 second timeout.
	Client *http.Client
}

type manageContact struct {
	Email      string            `json:"Email"`
	Name       string            `json:"Name,omitempty"`
	Action     string            `json:"Action"`
	Properties map[string]string `json:"Properties,omitempty"`
}

// Subscribe adds the address to the list, also when it unsubscribed before.
func (l List) Subscribe(ctx context.Context, email, name, locale string) error {
	contact := manageContact{Email: email, Name: name, Action: "addforce"}

	if l.LocaleProperty != "" && locale != "" {
		contact.Properties = map[string]string{l.LocaleProperty: locale}
	}

	return l.manage(ctx, contact)
}

// Unsubscribe marks the address as unsubscribed from the list.
func (l List) Unsubscribe(ctx context.Context, email, _, _ string) error {
	return l.manage(ctx, manageContact{Email: email, Action: "unsub"})
}

func (l List) manage(ctx context.Context, contact manageContact) error {
	if l.ListID <= 0 {
		return errors.New("mailjet: no contact list")
	}

	client := api{key: l.APIKey, secret: l.SecretKey, baseURL: l.BaseURL, client: l.Client}
	path := "/v3/REST/contactslist/" + strconv.FormatInt(l.ListID, 10) + "/managecontact"

	return client.post(ctx, path, contact, nil)
}
