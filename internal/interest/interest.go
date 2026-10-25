// Package interest lets signed-in customers mark interest in a published event.
// Organizers see only the count, never who is interested.
package interest

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("event not found")

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Get reports whether the user is interested in the published event.
func (s *Store) Get(ctx context.Context, userID, eventID string) (bool, error) {
	var published, interested bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE id = $1 AND status = 'PUBLISHED'),
		EXISTS (SELECT 1 FROM event_interests WHERE event_id = $1 AND user_id = $2)`, eventID, userID).Scan(&published, &interested)
	if err == nil && !published {
		err = ErrNotFound
	}
	return interested, err
}

// Set adds or removes the user's interest. Repeating either changes nothing.
func (s *Store) Set(ctx context.Context, userID, eventID string, interested bool) error {
	change := `DELETE FROM event_interests WHERE event_id = $1 AND user_id = $2`
	if interested {
		change = `INSERT INTO event_interests (tenant_id, event_id, user_id) SELECT tenant_id, id, $2::uuid FROM e ON CONFLICT DO NOTHING`
	}
	var published bool
	err := s.pool.QueryRow(ctx, `WITH e AS (SELECT tenant_id, id FROM events WHERE id = $1 AND status = 'PUBLISHED'),
		changed AS (`+change+` RETURNING 1)
		SELECT EXISTS (SELECT 1 FROM e)`, eventID, userID).Scan(&published)
	if err == nil && !published {
		err = ErrNotFound
	}
	return err
}
