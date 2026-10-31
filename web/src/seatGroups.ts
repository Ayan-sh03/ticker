import type { Seat } from './api';

// These arrays belong only to this snapshot; appending preserves the server's
// row/seat order without repeatedly copying each partially built row.
export function groupSeats(seats: Seat[]) {
  const groups = new Map<string, Seat[]>();
  for (const seat of seats) {
    const key = `${seat.section} / Row ${seat.row_number}`;
    let row = groups.get(key);
    if (!row) { row = []; groups.set(key, row); }
    row.push(seat);
  }
  return groups;
}
