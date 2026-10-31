// Package audit checks booking invariants that span several tables. Single-row
// and single-table rules are already enforced by constraints; these checks catch
// what only a whole-database view can: seats, reservations, bookings, and the
// audit trail disagreeing with each other. It reports problems and never repairs
// them, because a repair would have to guess who really owns a seat.
package audit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Options struct {
	// Worker is false for deployments that release overdue holds on demand,
	// where an overdue hold is expected rather than a sign of a stuck worker.
	Worker bool
	// HoldGrace is how long past its deadline a hold may wait for the worker.
	HoldGrace time.Duration
}

type Check struct {
	Name, Rule, query string
	args              []any
}

type Finding struct {
	Check
	Count    int64
	Examples []string
}

type Report struct {
	At                                time.Time
	Reservations, Bookings, Untracked int64
	Findings                          []Finding
	Skipped                           []Check
}

func (r Report) Failed() bool {
	for _, f := range r.Findings {
		if f.Count > 0 {
			return true
		}
	}
	return false
}

// tracked holds the reservations made since the audit trail began. The first
// reservation with a HELD entry marks the start; older ones have no history.
const tracked = `tracked AS (
	SELECT r.* FROM reservations r
	WHERE r.created_at >= (SELECT min(x.created_at) FROM reservations x
		WHERE EXISTS (SELECT 1 FROM audit_entries a WHERE a.reservation_id = x.id AND a.to_status = 'HELD')))`

func checks(o Options) (run, skipped []Check) {
	all := []Check{
		{Name: "seat_double_owned", Rule: "A seat has at most one active hold or confirmed owner.", query: `
			SELECT seat_id::text FROM reservation_seats WHERE released_at IS NULL
			GROUP BY seat_id HAVING count(*) > 1`},
		{Name: "seat_double_booked", Rule: "A seat appears in at most one active booking.", query: `
			SELECT seat_id::text FROM booking_seats WHERE released_at IS NULL GROUP BY seat_id HAVING count(*) > 1`},
		{Name: "sold_without_booking", Rule: "Every SOLD seat belongs to an active booking.", query: `
			SELECT s.id::text FROM seats s
			WHERE s.status = 'SOLD' AND NOT EXISTS (SELECT 1 FROM booking_seats b WHERE b.seat_id = s.id AND b.released_at IS NULL)`},
		{Name: "booked_not_sold", Rule: "Every seat of an active booking is SOLD.", query: `
			SELECT b.seat_id::text FROM booking_seats b JOIN seats s ON s.id = b.seat_id WHERE b.released_at IS NULL AND s.status <> 'SOLD'`},
		{Name: "blocked_seat_held", Rule: "A BLOCKED seat is never held.", query: `
			SELECT s.id::text FROM seats s WHERE s.status = 'BLOCKED' AND EXISTS (
				SELECT 1 FROM reservation_seats rs WHERE rs.seat_id = s.id AND rs.released_at IS NULL)`},
		{Name: "ended_reservation_owns_seats", Rule: "Cancelled and expired reservations have released every seat.", query: `
			SELECT DISTINCT r.id::text FROM reservations r JOIN reservation_seats rs ON rs.reservation_id = r.id
			WHERE r.status IN ('CANCELLED', 'EXPIRED') AND rs.released_at IS NULL`},
		{Name: "reservation_seat_count", Rule: "A reservation took exactly its quantity of seats, and owns all of them while held or confirmed.", query: `
			SELECT r.id::text FROM reservations r
			CROSS JOIN LATERAL (SELECT count(*) AS total, count(*) FILTER (WHERE released_at IS NULL) AS active
				FROM reservation_seats WHERE reservation_id = r.id) c
			WHERE c.total <> r.quantity OR (r.status IN ('HELD', 'CONFIRMED') AND c.active <> r.quantity)`},
		{Name: "confirmed_without_booking", Rule: "Every confirmed reservation has a booking.", query: `
			SELECT r.id::text FROM reservations r
			WHERE r.status = 'CONFIRMED' AND NOT EXISTS (SELECT 1 FROM bookings b WHERE b.reservation_id = r.id)`},
		{Name: "booking_without_confirmation", Rule: "An active booking's reservation is confirmed; a cancelled booking's is cancelled.", query: `
			SELECT b.id::text FROM bookings b JOIN reservations r ON r.id = b.reservation_id
			WHERE r.status <> CASE WHEN b.cancelled_at IS NULL THEN 'CONFIRMED' ELSE 'CANCELLED' END`},
		{Name: "booking_release_mismatch", Rule: "An active booking owns all its seats; a cancelled booking has released them all.", query: `
			SELECT DISTINCT b.id::text FROM bookings b JOIN booking_seats bs ON bs.booking_id = b.id
			WHERE (b.cancelled_at IS NULL) <> (bs.released_at IS NULL)`},
		{Name: "booking_seats_mismatch", Rule: "A booking covers exactly its reservation's seats.", query: `
			SELECT b.id::text FROM bookings b WHERE EXISTS (
				(SELECT seat_id FROM reservation_seats WHERE reservation_id = b.reservation_id
				 EXCEPT SELECT seat_id FROM booking_seats WHERE booking_id = b.id)
				UNION ALL
				(SELECT seat_id FROM booking_seats WHERE booking_id = b.id
				 EXCEPT SELECT seat_id FROM reservation_seats WHERE reservation_id = b.reservation_id))`},
		{Name: "booking_price_mismatch", Rule: "A booking's price matches its event, whose price froze at the first reservation.", query: `
			SELECT b.id::text FROM bookings b JOIN events e ON e.id = b.event_id
			WHERE (b.price_minor, b.currency) IS DISTINCT FROM (e.price_minor, e.currency)`},
		{Name: "overdue_hold", Rule: "The worker releases a hold soon after its deadline.", query: `
			SELECT id::text FROM reservations WHERE status = 'HELD' AND expires_at < now() - make_interval(secs => $1)`,
			args: []any{o.HoldGrace.Seconds()}},
		{Name: "expired_before_deadline", Rule: "A hold is never expired before its deadline.", query: `
			SELECT r.id::text FROM reservations r
			JOIN audit_entries a ON a.reservation_id = r.id AND a.to_status = 'EXPIRED'
			WHERE a.created_at < r.expires_at`},
		{Name: "audit_history_mismatch", Rule: "Each tracked reservation's entries, in order, are the one path to its status: held, then confirmed, expired, or cancelled; a cancelled booking was confirmed first.", query: `
			WITH ` + tracked + `
			SELECT t.id::text FROM tracked t
			CROSS JOIN LATERAL (SELECT string_agg(coalesce(from_status, '') || '>' || to_status, ',' ORDER BY id) AS path
				FROM audit_entries WHERE reservation_id = t.id) h
			WHERE h.path IS DISTINCT FROM CASE
				WHEN t.status = 'HELD' THEN '>HELD'
				WHEN EXISTS (SELECT 1 FROM bookings b WHERE b.reservation_id = t.id) THEN
					CASE t.status WHEN 'CONFIRMED' THEN '>HELD,HELD>CONFIRMED' ELSE '>HELD,HELD>CONFIRMED,CONFIRMED>CANCELLED' END
				ELSE '>HELD,HELD>' || t.status END`},
		{Name: "audit_actor_mismatch", Rule: "Customers hold, confirm, and cancel their own reservations; only the system expires them; whoever cancelled a booking, its customer or an organizer, is recorded on it.", query: `
			SELECT a.id::text FROM audit_entries a JOIN reservations r ON r.id = a.reservation_id
			LEFT JOIN bookings b ON b.reservation_id = r.id
			WHERE CASE WHEN a.to_status = 'EXPIRED'
				THEN a.actor_user_id IS NOT NULL OR a.reason <> 'deadline_passed'
				WHEN a.from_status = 'CONFIRMED'
				THEN a.actor_user_id IS DISTINCT FROM b.cancelled_by OR a.reason NOT IN ('organizer_request', 'customer_request')
					OR (a.reason = 'customer_request' AND a.actor_user_id IS DISTINCT FROM r.user_id)
				ELSE a.actor_user_id IS DISTINCT FROM r.user_id OR a.reason <> 'customer_request' END`},
	}
	for _, c := range all {
		if c.Name == "overdue_hold" && !o.Worker {
			skipped = append(skipped, c)
			continue
		}
		run = append(run, c)
	}
	return run, skipped
}

// Run checks every invariant inside one read-only snapshot, so rows committed
// while it runs cannot make two checks disagree.
func Run(ctx context.Context, pool *pgxpool.Pool, o Options) (Report, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(context.Background())
	var report Report
	if err := tx.QueryRow(ctx, `WITH `+tracked+`
		SELECT now(), (SELECT count(*) FROM reservations), (SELECT count(*) FROM bookings),
			(SELECT count(*) FROM reservations) - (SELECT count(*) FROM tracked)`).
		Scan(&report.At, &report.Reservations, &report.Bookings, &report.Untracked); err != nil {
		return Report{}, err
	}
	run, skipped := checks(o)
	report.Skipped = skipped
	for _, c := range run {
		finding := Finding{Check: c}
		if err := tx.QueryRow(ctx, `WITH found(subject) AS (`+c.query+`)
			SELECT count(*), coalesce((SELECT array_agg(subject) FROM
				(SELECT subject FROM found ORDER BY subject LIMIT 5) s), '{}') FROM found`, c.args...).
			Scan(&finding.Count, &finding.Examples); err != nil {
			return Report{}, err
		}
		report.Findings = append(report.Findings, finding)
	}
	return report, nil
}
