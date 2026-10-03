package queries

import _ "embed"

//go:embed lock_reservation_seat.sql
var LockReservationSeat string

//go:embed get_reservation.sql
var GetReservation string
