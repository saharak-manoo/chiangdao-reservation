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

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"

// vars so tests can point them at a fake server.
var (
	baseURL = "https://wildlifesanctuaryfca16.com"
	apiURL  = baseURL + "/api/chiangdao/"
)

var client = &http.Client{Timeout: 30 * time.Second}

type apiResp struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func main() {
	path := flag.String("file", "bookings.yaml", "YAML file with bookings (see bookings.example.yaml)")
	submit := flag.Bool("submit", false, "really submit; without it only prints what would be sent")
	check := flag.Bool("check", false, "only look up existing reservations for each booking (no booking made)")
	at := flag.String("at", "", `wait until this Bangkok time before submitting, e.g. "2026-10-17 09:00:00"`)
	only := flag.String("name", "", "only process the booking with this name")
	retries := flag.Int("retries", 5, "max attempts per booking on network/5xx errors")
	retryDelay := flag.Duration("retry-delay", 2*time.Second, "delay between attempts")
	watchMode := flag.Bool("watch", false, "keep running: wait for booking to open / seats to free up, book every booking, exit when all are booked (needs -submit to actually book)")
	interval := flag.Duration("interval", time.Minute, "-watch: time between checks (min 30s)")
	ignoreOpen := flag.Bool("ignore-open", false, "-watch: don't wait for /open to report open (use if that endpoint is broken while booking works)")
	notify := flag.Bool("notify", true, "-watch: macOS notification on open / booked / error")
	flag.Parse()

	f, err := os.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	bookings, err := loadBookings(f)
	_ = f.Close()
	if err != nil {
		log.Fatalf("%s: %v", *path, err)
	}

	if *only != "" {
		var kept []Booking
		for _, b := range bookings {
			if b.Name == *only {
				kept = append(kept, b)
			}
		}
		bookings = kept
	}
	if len(bookings) == 0 {
		log.Fatal("no bookings to process")
	}

	if !*check {
		today := time.Now().In(bangkok).Format("2006-01-02")
		for _, b := range bookings {
			if b.StartDate < today {
				log.Fatalf("[%s] start_date %s has already passed — fix or remove it", b.Name, b.StartDate)
			}
		}
	}

	if *check {
		for _, b := range bookings {
			printStatus(b)
		}
		return
	}

	if *watchMode {
		if *interval < 30*time.Second {
			log.Fatal("-interval must be at least 30s (don't hammer the site)")
		}
		watch(bookings, watchOpts{
			submit:     *submit,
			interval:   *interval,
			retries:    *retries,
			retryDelay: *retryDelay,
			ignoreOpen: *ignoreOpen,
			notify:     *notify,
		})
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
		if out, err := book(b, *retries, *retryDelay); out == outError || out == outNoSeats {
			log.Printf("[%s] FAILED: %v", b.Name, err)
			failed = append(failed, b.Name)
		}
	}
	if len(failed) > 0 {
		log.Fatalf("failed bookings: %s", strings.Join(failed, ", "))
	}
	log.Printf("all %d booking(s) submitted", len(bookings))
}

func printDryRun(b Booking) {
	fields, err := b.formFields()
	if err != nil {
		log.Fatalf("[%s] %v", b.Name, err)
	}
	fmt.Printf("\n=== %s: %s → %s, %d people ===\n", b.Name, b.StartDate, b.EndDate, len(b.People))
	for _, kv := range fields {
		fmt.Printf("  %-15s %s\n", kv[0], kv[1])
	}
}

func printStatus(b Booking) {
	phone, id := b.lookupKey()
	rs, err := checkReserve(phone, id)
	if err != nil {
		log.Printf("[%s] check-reserve failed: %v", b.Name, err)
		return
	}
	if len(rs) == 0 {
		log.Printf("[%s] no reservation found", b.Name)
		return
	}
	for _, r := range rs {
		mark := ""
		if bookingDate(r.StartDate) == b.StartDate {
			mark = "  ← matches start_date"
		}
		log.Printf("[%s] %s%s", b.Name, r, mark)
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

type outcome int

const (
	outBooked  outcome = iota // submitted and confirmed by check-reserve
	outAlready                // an active reservation for that date already existed
	outNoSeats                // not enough seats right now
	outError                  // anything else; err says what
)

func book(b Booking, retries int, delay time.Duration) (outcome, error) {
	phone, id := b.lookupKey()
	if rs, err := checkReserve(phone, id); err != nil {
		log.Printf("[%s] check-reserve failed (%v), submitting anyway", b.Name, err)
	} else if r := findByStartDate(rs, b.StartDate); r != nil {
		log.Printf("[%s] already booked, skipping: %s", b.Name, r)
		return outAlready, nil
	}

	if left, err := capacity(b.StartDate); err != nil {
		log.Printf("[%s] capacity check failed (%v), submitting anyway", b.Name, err)
	} else {
		log.Printf("[%s] %s: %d seat(s) left, need %d", b.Name, b.StartDate, left, len(b.People))
		if left < len(b.People) {
			return outNoSeats, fmt.Errorf("not enough seats on %s (%d left)", b.StartDate, left)
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
				log.Printf("[%s] form accepted: %s %s", b.Name, r.Message, trim(r.Data))
				if err := verifyBooked(b); err != nil {
					return outError, err
				}
				return outBooked, nil
			}
			// 4xx / business error (full, duplicate, bad data): retrying now won't help.
			return outError, fmt.Errorf("HTTP %d: %s", status, trim(body))
		}
		log.Printf("[%s] attempt %d/%d: %v", b.Name, attempt, retries, lastErr)
		if attempt < retries {
			time.Sleep(delay)
		}
	}
	return outError, lastErr
}

// verifyBooked confirms via check-reserve that the reservation really exists.
func verifyBooked(b Booking) error {
	phone, id := b.lookupKey()
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		rs, err := checkReserve(phone, id)
		if err == nil {
			if r := findByStartDate(rs, b.StartDate); r != nil {
				log.Printf("[%s] BOOKED ✔ %s", b.Name, r)
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
