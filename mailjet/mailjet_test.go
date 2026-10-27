package mailjet_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/donseba/go-webthings/mail"
	"github.com/donseba/go-webthings/modules/newsletter"

	"github.com/donseba/go-webthings-contrib/mailjet"
)

var (
	_ mail.Sender         = mailjet.Sender{}
	_ newsletter.Provider = mailjet.List{}
)

// call is one request the fake API received.
type call struct {
	Method string
	Path   string
	User   string
	Secret string
	Type   string
	Body   map[string]any
}

// fake answers every request with status and answer and records it.
func fake(t *testing.T, status int, answer string) (*httptest.Server, *[]call) {
	t.Helper()

	var calls []call

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, secret, _ := r.BasicAuth()
		data, _ := io.ReadAll(r.Body)

		received := call{
			Method: r.Method,
			Path:   r.URL.Path,
			User:   user,
			Secret: secret,
			Type:   r.Header.Get("Content-Type"),
		}

		if err := json.Unmarshal(data, &received.Body); err != nil {
			t.Errorf("request body: %v\n%s", err, data)
		}

		calls = append(calls, received)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(server.Close)

	return server, &calls
}

func TestSender(t *testing.T) {
	server, calls := fake(t, http.StatusOK, `{"Messages":[{"Status":"success","To":[{"Email":"jane@example.test"}]}]}`)
	sender := mailjet.Sender{
		APIKey:    "key",
		SecretKey: "secret",
		From:      "news@example.test",
		FromName:  "Example",
		BaseURL:   server.URL,
	}

	err := sender.Send(context.Background(), mail.Message{
		To:      []string{"Jane Doe <jane@example.test>", "joe@example.test"},
		ReplyTo: "office@example.test",
		Subject: "Hello",
		Text:    "Plain",
		HTML:    "<p>Rich</p>",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := call{
		Method: http.MethodPost,
		Path:   "/v3.1/send",
		User:   "key",
		Secret: "secret",
		Type:   "application/json",
		Body: map[string]any{
			"Messages": []any{map[string]any{
				"From": map[string]any{"Email": "news@example.test", "Name": "Example"},
				"To": []any{
					map[string]any{"Email": "jane@example.test", "Name": "Jane Doe"},
					map[string]any{"Email": "joe@example.test"},
				},
				"ReplyTo":  map[string]any{"Email": "office@example.test"},
				"Subject":  "Hello",
				"TextPart": "Plain",
				"HTMLPart": "<p>Rich</p>",
			}},
		},
	}

	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0], want) {
		t.Fatalf("request:\nwant %+v\ngot  %+v", want, *calls)
	}

	// Text-only mail has no HTML part and no reply address.
	if err := sender.Send(context.Background(), mail.Message{To: []string{"joe@example.test"}, Text: "Plain"}); err != nil {
		t.Fatal(err)
	}

	message := (*calls)[1].Body["Messages"].([]any)[0].(map[string]any)
	if _, ok := message["HTMLPart"]; ok {
		t.Fatalf("text-only message has an HTML part: %+v", message)
	}

	if _, ok := message["ReplyTo"]; ok {
		t.Fatalf("message has a reply address: %+v", message)
	}

	if err := sender.Send(context.Background(), mail.Message{Text: "Nobody"}); err == nil {
		t.Fatal("sent without recipients")
	}

	if err := sender.Send(context.Background(), mail.Message{To: []string{"x@example.test\r\nBcc: y"}}); err == nil {
		t.Fatal("sent with a header injection")
	}

	// Extra headers, such as a newsletter's unsubscribe link, are passed on.
	headers := map[string]string{
		"List-Unsubscribe":      "<https://example.test/_/news/unsubscribe?token=abc>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}

	if err := sender.Send(
		context.Background(),
		mail.Message{To: []string{"joe@example.test"}, Text: "News", Headers: headers},
	); err != nil {
		t.Fatal(err)
	}

	message = (*calls)[2].Body["Messages"].([]any)[0].(map[string]any)
	if got := message["Headers"]; !reflect.DeepEqual(got, map[string]any{
		"List-Unsubscribe":      headers["List-Unsubscribe"],
		"List-Unsubscribe-Post": headers["List-Unsubscribe-Post"],
	}) {
		t.Fatalf("headers: %+v", got)
	}

	for _, invalid := range []map[string]string{
		{"Bcc": "eve@example.test"},
		{"X-Note": "one\r\nBcc: eve@example.test"},
		{"X Note": "space"},
	} {
		if err := sender.Send(
			context.Background(),
			mail.Message{To: []string{"joe@example.test"}, Headers: invalid},
		); err == nil {
			t.Fatalf("sent with header %q", invalid)
		}
	}

	if len(*calls) != 3 {
		t.Fatalf("invalid headers reached Mailjet: %d calls", len(*calls))
	}
}

func TestSenderErrors(t *testing.T) {
	server, _ := fake(
		t,
		http.StatusBadRequest,
		`{"Messages":[{"Status":"error","Errors":[{"ErrorCode":"mj-0013","ErrorMessage":"\"x\" is an invalid email address."}]}]}`,
	)

	sender := mailjet.Sender{APIKey: "key", SecretKey: "secret", From: "news@example.test", BaseURL: server.URL}

	err := sender.Send(context.Background(), mail.Message{To: []string{"x@example.test"}, Text: "Hi"})

	var problem *mailjet.Error
	if !errors.As(err, &problem) || problem.Status != http.StatusBadRequest ||
		problem.Message != `"x" is an invalid email address.` {
		t.Fatalf("error: %v", err)
	}

	unauthorized, _ := fake(t, http.StatusUnauthorized, `{"ErrorMessage":"API key authentication/authorization failure"}`)
	sender.BaseURL = unauthorized.URL

	err = sender.Send(context.Background(), mail.Message{To: []string{"x@example.test"}, Text: "Hi"})
	if !errors.As(err, &problem) || problem.Status != http.StatusUnauthorized {
		t.Fatalf("unauthorized: %v", err)
	}

	// A 200 answer can still report a message that was not accepted.
	refused, _ := fake(t, http.StatusOK, `{"Messages":[{"Status":"error"}]}`)
	sender.BaseURL = refused.URL

	if err := sender.Send(context.Background(), mail.Message{To: []string{"x@example.test"}, Text: "Hi"}); err == nil {
		t.Fatal("refused message reported as sent")
	}
}

func TestList(t *testing.T) {
	server, calls := fake(t, http.StatusCreated, `{"Count":1,"Data":[{"ContactID":1}],"Total":1}`)
	list := mailjet.List{APIKey: "key", SecretKey: "secret", ListID: 42, LocaleProperty: "language", BaseURL: server.URL}
	ctx := context.Background()

	if err := list.Subscribe(ctx, "jane@example.test", "Jane", "nl"); err != nil {
		t.Fatal(err)
	}

	if err := list.Unsubscribe(ctx, "jane@example.test", "Jane", "nl"); err != nil {
		t.Fatal(err)
	}

	want := []call{
		{
			Method: http.MethodPost,
			Path:   "/v3/REST/contactslist/42/managecontact",
			User:   "key",
			Secret: "secret",
			Type:   "application/json",
			Body: map[string]any{
				"Email":      "jane@example.test",
				"Name":       "Jane",
				"Action":     "addforce",
				"Properties": map[string]any{"language": "nl"},
			},
		},
		{
			Method: http.MethodPost,
			Path:   "/v3/REST/contactslist/42/managecontact",
			User:   "key",
			Secret: "secret",
			Type:   "application/json",
			Body:   map[string]any{"Email": "jane@example.test", "Action": "unsub"},
		},
	}

	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("requests:\nwant %+v\ngot  %+v", want, *calls)
	}

	failing, _ := fake(t, http.StatusBadRequest, `{"ErrorInfo":"","ErrorMessage":"Invalid email","StatusCode":400}`)
	list.BaseURL = failing.URL

	var problem *mailjet.Error
	if err := list.Subscribe(ctx, "bad", "", ""); !errors.As(err, &problem) || problem.Message != "Invalid email" {
		t.Fatalf("error: %v", err)
	}

	if err := (mailjet.List{}).Subscribe(ctx, "jane@example.test", "", ""); err == nil {
		t.Fatal("subscribed without a list")
	}
}
