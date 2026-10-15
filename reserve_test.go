package main

import "testing"

func TestBookingDate(t *testing.T) {
	cases := map[string]string{
		"2026-01-31":                "2026-01-31",
		"2026-01-31 00:00:00":       "2026-01-31",
		"2026-01-30T17:00:00.000Z":  "2026-01-31", // UTC midnight-Bangkok
		"2026-01-31T00:00:00+07:00": "2026-01-31",
	}
	for in, want := range cases {
		if got := bookingDate(in); got != want {
			t.Errorf("bookingDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseReservations(t *testing.T) {
	body := []byte(`{"status":"success","message":"done","data":[
		{"id":42,"reservation_no":"CD-001","status":"WAIT","start_date":"2026-01-30T17:00:00.000Z","end_date":"2026-02-01","qty_people":2,"service_total":"1500.00","expire_at":"2026-01-01 12:00"}]}`)
	rs, err := parseReservations(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Status != "WAIT" || rs[0].ReservationNo != "CD-001" {
		t.Fatalf("got %+v", rs)
	}
	if r := findByStartDate(rs, "2026-01-31"); r == nil {
		t.Fatal("should match start date across timezone")
	}
	if r := findByStartDate(rs, "2026-02-07"); r != nil {
		t.Fatal("should not match other date")
	}
	rs[0].Status = "CANCEL"
	if r := findByStartDate(rs, "2026-01-31"); r != nil {
		t.Fatal("cancelled reservation must not count as booked")
	}
}

func TestParseReservationsEmpty(t *testing.T) {
	rs, err := parseReservations([]byte(`{"status":"success","message":"done","data":[]}`))
	if err != nil || len(rs) != 0 {
		t.Fatalf("got %v, %v", rs, err)
	}
}
