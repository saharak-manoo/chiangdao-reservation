package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Reservation is one item from GET reservation/check-reserve.
// Status: WAIT = not paid yet, WAIT_APPROVE = slip uploaded/waiting review, PAID = done, CANCEL = cancelled/expired.
type Reservation struct {
	ID            any    `json:"id"`
	ReservationNo string `json:"reservation_no"`
	Status        string `json:"status"`
	StartDate     string `json:"start_date"`
	EndDate       string `json:"end_date"`
	QtyPeople     any    `json:"qty_people"`
	ServiceTotal  string `json:"service_total"`
	ExpireAt      string `json:"expire_at"`
}

func (r Reservation) String() string {
	return fmt.Sprintf("no=%s id=%v status=%s %s→%s people=%v total=%s expire=%s",
		r.ReservationNo, r.ID, r.Status, bookingDate(r.StartDate), bookingDate(r.EndDate),
		r.QtyPeople, r.ServiceTotal, r.ExpireAt)
}

// checkReserve looks up reservations by the main booker's phone + ID card (or passport),
// same call as the site's /chiangdao/reservationStatus page.
func checkReserve(phone, idCard string) ([]Reservation, error) {
	q := url.Values{"phone": {phone}, "id_card_number": {idCard}}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		apiURL+"reservation/check-reserve?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, err
	}
	setHeaders(req)
	req.Header.Set("Referer", baseURL+"/chiangdao/reservationStatus")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, trim(body))
	}
	return parseReservations(body)
}

func parseReservations(body []byte) ([]Reservation, error) {
	var r struct {
		Status string        `json:"status"`
		Data   []Reservation `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode %s: %w", trim(body), err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("status %q: %s", r.Status, trim(body))
	}
	return r.Data, nil
}

// findByStartDate returns the active (non-cancelled) reservation for startDate, if any.
func findByStartDate(rs []Reservation, startDate string) *Reservation {
	for i := range rs {
		if rs[i].Status != "CANCEL" && bookingDate(rs[i].StartDate) == startDate {
			return &rs[i]
		}
	}
	return nil
}

// bookingDate normalises whatever date format the API returns to YYYY-MM-DD in Bangkok time
// (an ISO timestamp in UTC can be the previous day).
func bookingDate(s string) string {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.In(bangkok).Format("2006-01-02")
		}
	}
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// lookupKey is what check-reserve needs for a booking: the first person's phone + ID card (or passport).
func (b Booking) lookupKey() (phone, id string) {
	p := b.People[0]
	return p.Phone, or(p.IDCard, p.Passport)
}
