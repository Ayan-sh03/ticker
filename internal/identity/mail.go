package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// ErrNotSent means the provider answered and refused the email. Any other
// error, such as a timeout, may hide an email that was sent anyway.
var ErrNotSent = errors.New("email was not sent")

// ResendMailer sends codes through the Resend HTTP API.
type ResendMailer struct {
	apiKey, from string
	client       *http.Client
}

func NewResendMailer(apiKey, from string) *ResendMailer {
	return &ResendMailer{apiKey: apiKey, from: from, client: &http.Client{Timeout: 5 * time.Second}}
}

func (m *ResendMailer) SendCode(ctx context.Context, to, code string) error {
	body, _ := json.Marshal(map[string]any{
		"from":    m.from,
		"to":      []string{to},
		"subject": code + " is your Ticker code",
		"text": "Your Ticker verification code is " + code + ".\n\n" +
			"It expires in 10 minutes. If you did not try to create a Ticker account, you can ignore this email.",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: resend returned %d: %s", ErrNotSent, resp.StatusCode, detail)
	}
	return nil
}

// LogMailer writes codes to the API log. It is for local development only.
type LogMailer struct{ Logger *slog.Logger }

func (m LogMailer) SendCode(_ context.Context, to, code string) error {
	m.Logger.Info("signup code (development mailer)", "to", to, "code", code)
	return nil
}
