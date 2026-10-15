package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSite struct {
	mu          sync.Mutex
	openCalls   int
	openAfter   int // /open returns 500 for this many calls
	qtyCalls    int
	seatsAfter  int // check-qty-people reports 0 for this many calls after open
	formStatus  int
	formBody    string
	formPosts   []time.Time
	booked      bool
	clock       func() time.Time
	reserveDate string
}

func (f *fakeSite) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/open"):
		f.openCalls++
		if f.openCalls <= f.openAfter {
			w.WriteHeader(500)
			fmt.Fprint(w, "Something broke!")
			return
		}
		fmt.Fprint(w, `{"status":"success","data":{"open":true}}`)
	case strings.HasSuffix(r.URL.Path, "/reservation/check-qty-people"):
		f.qtyCalls++
		seats := 10
		if f.qtyCalls <= f.seatsAfter {
			seats = 0
		}
		fmt.Fprintf(w, `{"status":"success","data":{"qty":100,"qty_people":%d,"qty_reserve":%d}}`, 100-seats, seats)
	case strings.HasSuffix(r.URL.Path, "/reservation/check-reserve"):
		if f.booked {
			fmt.Fprintf(w, `{"status":"success","data":[{"id":1,"reservation_no":"T1","status":"WAIT","start_date":%q}]}`, f.reserveDate)
			return
		}
		fmt.Fprint(w, `{"status":"success","data":[]}`)
	case strings.HasSuffix(r.URL.Path, "/reservation/form"):
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("enc") == "" {
			w.WriteHeader(400)
			return
		}
		f.formPosts = append(f.formPosts, f.clock())
		w.WriteHeader(f.formStatus)
		fmt.Fprint(w, f.formBody)
		if f.formStatus == 200 {
			f.booked = true
		}
	default:
		w.WriteHeader(404)
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time        { return c.t }
func (c *fakeClock) sleep(d time.Duration) { c.t = c.t.Add(d) }
func startFakeSite(t *testing.T, f *fakeSite) {
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	oldBase, oldAPI := baseURL, apiURL
	baseURL, apiURL = srv.URL, srv.URL+"/api/chiangdao/"
	t.Cleanup(func() { baseURL, apiURL = oldBase, oldAPI })
}

func testBooking(t *testing.T) Booking {
	bs, err := loadBookings(strings.NewReader(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	return bs[0] // start_date 2026-01-31
}

func TestWatchWaitsForOpenAndSeatsThenBooksOnce(t *testing.T) {
	clock := &fakeClock{time.Date(2026, 1, 20, 9, 0, 0, 0, bangkok)}
	site := &fakeSite{openAfter: 3, seatsAfter: 2, formStatus: 200, formBody: `{"status":"success","message":"done"}`,
		clock: clock.now, reserveDate: "2026-01-31"}
	startFakeSite(t, site)

	var alerts []string
	watch([]Booking{testBooking(t)}, watchOpts{
		submit: true, interval: time.Minute, retries: 1,
		sleep: clock.sleep, now: clock.now,
		alert: func(title, _ string) { alerts = append(alerts, title) },
	})

	if len(site.formPosts) != 1 {
		t.Fatalf("form posted %d times, want 1", len(site.formPosts))
	}
	if site.openCalls < 4 {
		t.Fatalf("should have polled /open until it opened, got %d calls", site.openCalls)
	}
	joined := strings.Join(alerts, "|")
	if !strings.Contains(joined, "เปิดจองแล้ว") || !strings.Contains(joined, "จองสำเร็จ") {
		t.Fatalf("alerts = %v", alerts)
	}
}

func TestWatchBacksOffOnRejectedForm(t *testing.T) {
	clock := &fakeClock{time.Date(2026, 1, 30, 9, 0, 0, 0, bangkok)}
	site := &fakeSite{formStatus: 400, formBody: `{"status":"error","message":"full"}`, clock: clock.now}
	startFakeSite(t, site)

	var alerts []string
	watch([]Booking{testBooking(t)}, watchOpts{
		submit: true, interval: time.Minute, retries: 1,
		sleep: clock.sleep, now: clock.now,
		alert: func(title, _ string) { alerts = append(alerts, title) },
	})

	// runs until the start date passes (~39h), so must have retried, but never faster than the backoff
	if len(site.formPosts) < 2 {
		t.Fatalf("should keep retrying, got %d posts", len(site.formPosts))
	}
	for i := 1; i < len(site.formPosts); i++ {
		if gap := site.formPosts[i].Sub(site.formPosts[i-1]); gap < minBackoff {
			t.Fatalf("post %d only %s after previous", i, gap)
		}
	}
	errAlerts := 0
	for _, a := range alerts {
		if a == "เชียงดาว: จองไม่สำเร็จ" {
			errAlerts++
		}
	}
	if errAlerts != 1 {
		t.Fatalf("same error should alert once, got %d", errAlerts)
	}
	if !strings.Contains(strings.Join(alerts, "|"), "เลยวันแล้ว") {
		t.Fatalf("should alert when date passes: %v", alerts)
	}
}

func TestWatchOnlyNeverPostsForm(t *testing.T) {
	clock := &fakeClock{time.Date(2026, 1, 30, 22, 0, 0, 0, bangkok)}
	site := &fakeSite{formStatus: 200, clock: clock.now}
	startFakeSite(t, site)

	var alerts []string
	watch([]Booking{testBooking(t)}, watchOpts{
		submit: false, interval: time.Minute, retries: 1,
		sleep: clock.sleep, now: clock.now,
		alert: func(title, _ string) { alerts = append(alerts, title) },
	})
	if len(site.formPosts) != 0 {
		t.Fatalf("watch-only must not post, got %d", len(site.formPosts))
	}
	seatAlerts := 0
	for _, a := range alerts {
		if a == "เชียงดาว: มีที่ว่าง" {
			seatAlerts++
		}
	}
	if seatAlerts != 1 {
		t.Fatalf("unchanged seat count should alert once, got %d", seatAlerts)
	}
}

func TestParseOpen(t *testing.T) {
	if ok, _ := parseOpen([]byte(`{"status":"success","data":{"open":true}}`)); !ok {
		t.Fatal("open:true should be open")
	}
	for _, body := range []string{`{"data":{"open":false}}`, `{"data":[]}`, `Something broke!`} {
		if ok, _ := parseOpen([]byte(body)); ok {
			t.Fatalf("%s should be closed", body)
		}
	}
}
