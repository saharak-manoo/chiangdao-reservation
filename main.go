package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	baseURL   = "https://wildlifesanctuaryfca16.com"
	apiURL    = baseURL + "/api/chiangdao/"
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
)

var client = &http.Client{Timeout: 30 * time.Second}

type apiResp struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func main() {
	csvPath := flag.String("csv", "people.csv", "CSV file (one row per person, rows with the same group = one booking)")
	submit := flag.Bool("submit", false, "really submit; without it only prints what would be sent")
	check := flag.Bool("check", false, "only look up existing reservations for each group (no booking)")
	at := flag.String("at", "", `wait until this Bangkok time before submitting, e.g. "2026-10-17 09:00:00"`)
	only := flag.String("group", "", "only process this group")
	retries := flag.Int("retries", 5, "max attempts per booking on network/5xx errors")
	retryDelay := flag.Duration("retry-delay", 2*time.Second, "delay between attempts")
	flag.Parse()

	f, err := os.Open(*csvPath)
	if err != nil {
		log.Fatal(err)
	}
	bookings, err := loadBookings(f)
	_ = f.Close()
	if err != nil {
		log.Fatalf("%s: %v", *csvPath, err)
	}

	if *only != "" {
		var kept []Booking
		for _, b := range bookings {
			if b.Group == *only {
				kept = append(kept, b)
			}
		}
		bookings = kept
	}
	if len(bookings) == 0 {
		log.Fatal("no bookings to process")
	}

	if *check {
		for _, b := range bookings {
			printStatus(b)
		}
		return
	}

	if !*submit {
		for _, b := range bookings {
			printDryRun(b)
		}
		fmt.Println("\nDRY RUN — nothing sent. Add -submit to book for real.")
		return
	}

	if *at != "" {
		waitUntil(*at)
	}

	var failed []string
	for i, b := range bookings {
		if i > 0 {
			time.Sleep(time.Second)
		}
		if err := book(b, *retries, *retryDelay); err != nil {
			log.Printf("[%s] FAILED: %v", b.Group, err)
			failed = append(failed, b.Group)
		}
	}
	if len(failed) > 0 {
		log.Fatalf("failed groups: %s", strings.Join(failed, ", "))
	}
	log.Printf("all %d booking(s) submitted", len(bookings))
}

func printDryRun(b Booking) {
	fields, err := b.formFields()
	if err != nil {
		log.Fatalf("[%s] %v", b.Group, err)
	}
	fmt.Printf("\n=== group %s: %s → %s, %d people ===\n", b.Group, b.StartDate, b.EndDate, len(b.People))
	for _, kv := range fields {
		fmt.Printf("  %-15s %s\n", kv[0], kv[1])
	}
}

func printStatus(b Booking) {
	phone, id := b.lookupKey()
	rs, err := checkReserve(phone, id)
	if err != nil {
		log.Printf("[%s] check-reserve failed: %v", b.Group, err)
		return
	}
	if len(rs) == 0 {
		log.Printf("[%s] no reservation found", b.Group)
		return
	}
	for _, r := range rs {
		mark := ""
		if bookingDate(r.StartDate) == b.StartDate {
			mark = "  ← matches CSV start_date"
		}
		log.Printf("[%s] %s%s", b.Group, r, mark)
	}
}

func waitUntil(s string) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, bangkok)
	if err != nil {
		log.Fatalf(`-at must look like "2006-01-02 15:04:05" (Bangkok time): %v`, err)
	}
	d := time.Until(t)
	if d <= 0 {
		log.Printf("-at %s already passed, submitting now", s)
		return
	}
	log.Printf("waiting %s until %s ...", d.Round(time.Second), s)
	time.Sleep(d)
}

func book(b Booking, retries int, delay time.Duration) error {
	phone, id := b.lookupKey()
	if rs, err := checkReserve(phone, id); err != nil {
		log.Printf("[%s] check-reserve failed (%v), submitting anyway", b.Group, err)
	} else if r := findByStartDate(rs, b.StartDate); r != nil {
		log.Printf("[%s] already booked, skipping: %s", b.Group, r)
		return nil
	}

	if left, err := capacity(b.StartDate); err != nil {
		log.Printf("[%s] capacity check failed (%v), submitting anyway", b.Group, err)
	} else {
		log.Printf("[%s] %s: %d seat(s) left, need %d", b.Group, b.StartDate, left, len(b.People))
		if left < len(b.People) {
			return fmt.Errorf("not enough seats on %s (%d left)", b.StartDate, left)
		}
	}

	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		status, body, err := postForm(b)
		switch {
		case err != nil:
			lastErr = err
		case status >= 500:
			lastErr = fmt.Errorf("HTTP %d: %s", status, trim(body))
		default:
			var r apiResp
			if json.Unmarshal(body, &r) == nil && r.Status == "success" {
				log.Printf("[%s] form accepted: %s %s", b.Group, r.Message, trim(r.Data))
				return verifyBooked(b)
			}
			// 4xx / business error (full, duplicate, bad data): retrying won't help.
			return fmt.Errorf("HTTP %d: %s", status, trim(body))
		}
		log.Printf("[%s] attempt %d/%d: %v", b.Group, attempt, retries, lastErr)
		if attempt < retries {
			time.Sleep(delay)
		}
	}
	return lastErr
}

// verifyBooked confirms via check-reserve that the reservation really exists.
func verifyBooked(b Booking) error {
	phone, id := b.lookupKey()
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		rs, err := checkReserve(phone, id)
		if err == nil {
			if r := findByStartDate(rs, b.StartDate); r != nil {
				log.Printf("[%s] BOOKED ✔ %s", b.Group, r)
				return nil
			}
			err = fmt.Errorf("no reservation for %s yet (%d other)", b.StartDate, len(rs))
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("form accepted but not confirmed by check-reserve: %w — check manually with -check", lastErr)
}

func capacity(startDate string) (int, error) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		apiURL+"reservation/check-qty-people?start_date="+url.QueryEscape(startDate), http.NoBody)
	setHeaders(req)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var r struct {
		Status string `json:"status"`
		Data   struct {
			QtyReserve int `json:"qty_reserve"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, err
	}
	if r.Status != "success" {
		return 0, fmt.Errorf("status %q", r.Status)
	}
	return r.Data.QtyReserve, nil
}

func postForm(b Booking) (int, []byte, error) {
	fields, err := b.formFields() // fresh enc each attempt, like the browser
	if err != nil {
		return 0, nil, err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, kv := range fields {
		if err := w.WriteField(kv[0], kv[1]); err != nil {
			return 0, nil, err
		}
	}
	if err := w.Close(); err != nil {
		return 0, nil, err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, apiURL+"reservation/form", &buf)
	if err != nil {
		return 0, nil, err
	}
	setHeaders(req)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "th,en-US;q=0.9,en;q=0.8")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/chiangdao/reservation")
	req.Header.Set("User-Agent", userAgent)
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	return s
}
