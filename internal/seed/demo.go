package seed

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Summary struct {
	Dataset     string   `json:"dataset"`
	Seed        int64    `json:"seed"`
	TenantID    string   `json:"tenant_id"`
	EventID     string   `json:"event_id"`
	OrganizerID string   `json:"organizer_id"`
	CustomerIDs []string `json:"customer_ids"`
	SeatCount   int      `json:"seat_count"`
}

func Demo(ctx context.Context, pool *pgxpool.Pool, value int64) (Summary, error) {
	if value < 0 {
		return Summary{}, errors.New("seed must be non-negative")
	}
	summary := Summary{
		Dataset:     "demo",
		Seed:        value,
		TenantID:    fixtureID(value, "tenant"),
		EventID:     fixtureID(value, "event"),
		OrganizerID: fixtureID(value, "organizer"),
		CustomerIDs: []string{fixtureID(value, "customer-1"), fixtureID(value, "customer-2")},
		SeatCount:   24,
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Summary{}, err
	}
	defer tx.Rollback(context.Background())
	createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	users := []struct{ id, email, name string }{
		{summary.OrganizerID, fmt.Sprintf("organizer.%d@demo.ticker.local", value), "Demo Organizer"},
		{summary.CustomerIDs[0], fmt.Sprintf("customer1.%d@demo.ticker.local", value), "Demo Customer One"},
		{summary.CustomerIDs[1], fmt.Sprintf("customer2.%d@demo.ticker.local", value), "Demo Customer Two"},
	}
	for _, user := range users {
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (id, email, display_name, created_at) VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO NOTHING`, user.id, user.email, user.name, createdAt); err != nil {
			return Summary{}, fmt.Errorf("seed user: %w", err)
		}
		var matches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM users WHERE id = $1 AND email = $2 AND display_name = $3
		)`, user.id, user.email, user.name).Scan(&matches); err != nil {
			return Summary{}, err
		}
		if !matches {
			return Summary{}, errors.New("existing demo user differs from the fixture; no data was overwritten")
		}
	}
	slug := fmt.Sprintf("city-concerts-demo-%d", value)
	if _, err := tx.Exec(ctx, `
		INSERT INTO tenants (id, slug, name, created_at) VALUES ($1, $2, 'City Concerts', $3)
		ON CONFLICT (id) DO NOTHING`, summary.TenantID, slug, createdAt); err != nil {
		return Summary{}, fmt.Errorf("seed tenant: %w", err)
	}
	var matches bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM tenants WHERE id = $1 AND slug = $2 AND name = 'City Concerts'
	)`, summary.TenantID, slug).Scan(&matches); err != nil {
		return Summary{}, err
	}
	if !matches {
		return Summary{}, errors.New("existing demo tenant differs from the fixture; no data was overwritten")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (tenant_id, user_id, created_at) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, user_id) DO NOTHING`, summary.TenantID, summary.OrganizerID, createdAt); err != nil {
		return Summary{}, fmt.Errorf("seed membership: %w", err)
	}
	startsAt := time.Date(2030, 1, 12, 13, 30, 0, 0, time.UTC)
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, tenant_id, title, description, venue_name, price_minor,
			currency, sales_open_at, starts_at, layout_rows, layout_columns, created_at)
		VALUES ($1, $2, 'Friday Live', 'Deterministic demo event', 'City Hall', 50000,
			'INR', $3, $4, 3, 8, $3)
		ON CONFLICT (id) DO NOTHING`, summary.EventID, summary.TenantID, createdAt, startsAt); err != nil {
		return Summary{}, fmt.Errorf("seed event: %w", err)
	}
	// Serialize repeated seeds before inspecting or filling the layout.
	var status string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM events WHERE id = $1 AND tenant_id = $2
		AND title = 'Friday Live' AND description = 'Deterministic demo event' AND venue_name = 'City Hall'
		AND price_minor = 50000 AND currency = 'INR' AND sales_open_at = $3 AND starts_at = $4
		AND layout_rows = 3 AND layout_columns = 8 FOR UPDATE`,
		summary.EventID, summary.TenantID, createdAt, startsAt).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Summary{}, errors.New("existing demo event differs from the fixture; no data was overwritten")
		}
		return Summary{}, err
	}
	for row := 1; row <= 3; row++ {
		for number := 1; number <= 8; number++ {
			label := fmt.Sprintf("%c-%d", 'A'+row-1, number)
			id := fixtureID(value, "seat/"+label)
			// Avoid INSERT triggers for existing published seats. Never reset availability.
			if _, err := tx.Exec(ctx, `
				INSERT INTO seats (id, tenant_id, event_id, section, row_number, seat_number, label, created_at)
				SELECT $1::uuid, $2::uuid, $3::uuid, 'Main', $4, $5, $6, $7
				WHERE NOT EXISTS (SELECT 1 FROM seats WHERE id = $1)
				ON CONFLICT (id) DO NOTHING`, id, summary.TenantID, summary.EventID, row, number, label, createdAt); err != nil {
				return Summary{}, fmt.Errorf("seed seat %s: %w", label, err)
			}
			if err := tx.QueryRow(ctx, `SELECT EXISTS (
				SELECT 1 FROM seats WHERE id = $1 AND tenant_id = $2 AND event_id = $3
				AND section = 'Main' AND row_number = $4 AND seat_number = $5 AND label = $6
			)`, id, summary.TenantID, summary.EventID, row, number, label).Scan(&matches); err != nil {
				return Summary{}, err
			}
			if !matches {
				return Summary{}, fmt.Errorf("existing demo seat %s differs from the fixture; no data was overwritten", label)
			}
		}
	}
	var seatCount int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM seats WHERE event_id = $1", summary.EventID).Scan(&seatCount); err != nil {
		return Summary{}, err
	}
	if seatCount != summary.SeatCount {
		return Summary{}, errors.New("existing demo layout contains unexpected seats; no data was overwritten")
	}
	if status == "DRAFT" {
		if _, err := tx.Exec(ctx, "UPDATE events SET status = 'PUBLISHED' WHERE id = $1", summary.EventID); err != nil {
			return Summary{}, fmt.Errorf("publish demo event: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Summary{}, fmt.Errorf("commit demo seed: %w", err)
	}
	return summary, nil
}

func fixtureID(value int64, name string) string {
	hash := sha256.Sum256(fmt.Appendf(nil, "ticker/demo/v1/%d/%s", value, name))
	// UUID v8: deterministic custom hash with the standard UUID variant.
	hash[6] = (hash[6] & 0x0f) | 0x80
	hash[8] = (hash[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", hash[:4], hash[4:6], hash[6:8], hash[8:10], hash[10:16])
}
