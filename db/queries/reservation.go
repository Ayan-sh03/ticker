package queries

import _ "embed"

//go:embed lock_reservation_seats.sql
var LockReservationSeats string

//go:embed get_reservation.sql
var GetReservation string
