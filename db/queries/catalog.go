package queries

import _ "embed"

//go:embed list_published_events.sql
var ListPublishedEvents string

//go:embed get_published_event.sql
var GetPublishedEvent string

//go:embed list_event_seats.sql
var ListEventSeats string
