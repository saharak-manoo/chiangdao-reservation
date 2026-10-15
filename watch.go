package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

type watchOpts struct {
	submit     bool
	interval   time.Duration
	retries    int
	retryDelay time.Duration
	ignoreOpen bool
	notify     bool

	// Until fastUntil, poll every fastInterval and back off errors only fastInterval*3:
	// seats at opening time go in minutes.
	fastInterval time.Duration
	fastUntil    time.Time

	// test hooks
	sleep func(time.Duration)
	now   func() time.Time
	alert func(title, msg string)
}

const (
	minBackoff = 5 * time.Minute
	maxBackoff = 30 * time.Minute
)

type watchState struct {
	done      bool
	nextTry   time.Time
	backoff   time.Duration
	lastError string
	lastSeats string
}

// watch loops until every booking is booked (or its date has passed):
// wait for /open, then per booking check-reserve → seats → submit → verify.
// Errors back off 5m→30m per booking so a rejected form isn't resent every tick.
func watch(bookings []Booking, o watchOpts) {
	if o.sleep == nil {
		o.sleep = time.Sleep
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.alert == nil {
		o.alert = func(title, msg string) {
			if o.notify {
				macNotify(title, msg)
			}
		}
	}
	mode := "WATCH ONLY (no -submit: will notify, not book)"
	if o.submit {
		mode = "AUTO-BOOK"
	}
	log.Printf("watch: %s, %d booking(s), every %s", mode, len(bookings), o.interval)

	states := make([]watchState, len(bookings))
	wasOpen := false
	for {
		today := o.now().In(bangkok).Format("2006-01-02")
		pending := 0
		for i, b := range bookings {
			if states[i].done {
				continue
			}
			if b.StartDate < today {
				states[i].done = true
				log.Printf("[%s] %s has passed, giving up", b.Name, b.StartDate)
				o.alert("เชียงดาว: เลยวันแล้ว", fmt.Sprintf("%s (%s) จองไม่ทัน", b.Name, b.StartDate))
				continue
			}
			pending++
		}
		if pending == 0 {
			log.Print("watch: nothing left to do")
			return
		}

		open := true
		if !o.ignoreOpen {
			var err error
			open, err = isOpen()
			if err != nil {
				log.Printf("watch: booking closed (%v)", err)
			}
		}
		if open && !wasOpen {
			log.Print("watch: booking is OPEN")
			if !o.ignoreOpen {
				o.alert("เชียงดาว: เปิดจองแล้ว", fmt.Sprintf("กำลังจอง %d รายการ", pending))
			}
		}
		wasOpen = open

		if open {
			first := true
			for i, b := range bookings {
				st := &states[i]
				if st.done || o.now().Before(st.nextTry) {
					continue
				}
				if !first {
					o.sleep(time.Second)
				}
				first = false
				tick(b, st, o)
			}
		}

		// ±10% jitter so we don't hit the site on an exact beat.
		iv := o.interval
		if o.fast() {
			iv = o.fastInterval
		}
		o.sleep(time.Duration(float64(iv) * (0.9 + 0.2*rand.Float64())))
	}
}

func (o watchOpts) fast() bool {
	return o.fastInterval > 0 && o.now().Before(o.fastUntil)
}

func tick(b Booking, st *watchState, o watchOpts) {
	if !o.submit {
		left, err := capacity(b.StartDate)
		if err != nil {
			log.Printf("[%s] capacity check failed: %v", b.Name, err)
			return
		}
		seats := fmt.Sprintf("%d", left)
		if left >= len(b.People) && seats != st.lastSeats {
			o.alert("เชียงดาว: มีที่ว่าง", fmt.Sprintf("%s %s ว่าง %d ที่", b.Name, b.StartDate, left))
		}
		log.Printf("[%s] %s: %d seat(s) left, need %d", b.Name, b.StartDate, left, len(b.People))
		st.lastSeats = seats
		return
	}

	out, err := book(b, o.retries, o.retryDelay)
	switch out {
	case outBooked:
		st.done = true
		o.alert("เชียงดาว: จองสำเร็จ ✔", fmt.Sprintf("%s %s — รีบชำระเงินก่อนหมดเวลา", b.Name, b.StartDate))
	case outAlready:
		st.done = true
	case outNoSeats:
		// normal while waiting for someone to drop out; check again next tick
	case outError:
		switch {
		case o.fast():
			st.backoff = o.fastInterval * 3
		case st.backoff == 0:
			st.backoff = minBackoff
		default:
			st.backoff = min(st.backoff*2, maxBackoff)
		}
		st.nextTry = o.now().Add(st.backoff)
		log.Printf("[%s] error: %v — retry in %s", b.Name, err, st.backoff)
		if msg := err.Error(); msg != st.lastError {
			st.lastError = msg
			o.alert("เชียงดาว: จองไม่สำเร็จ", fmt.Sprintf("%s: %s", b.Name, trimStr(msg, 150)))
		}
		return
	}
	st.backoff, st.lastError = 0, ""
}

// isOpen mirrors the site's reservation page: it shows the form only when
// GET /open returns 200 and data.open == true.
func isOpen() (bool, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, apiURL+"open", http.NoBody)
	if err != nil {
		return false, err
	}
	setHeaders(req)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("/open HTTP %d: %s", resp.StatusCode, trimStr(string(body), 100))
	}
	return parseOpen(body)
}

func parseOpen(body []byte) (bool, error) {
	var r struct {
		Data struct {
			Open bool `json:"open"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return false, fmt.Errorf("/open: unexpected body %s", trimStr(string(body), 200))
	}
	if !r.Data.Open {
		return false, fmt.Errorf("/open says closed: %s", trimStr(string(body), 200))
	}
	return true, nil
}

func macNotify(title, msg string) {
	if runtime.GOOS != "darwin" {
		return
	}
	// Pass text as argv so quotes in messages can't break the script.
	cmd := exec.CommandContext(context.Background(), "osascript",
		"-e", "on run argv",
		"-e", `display notification (item 2 of argv) with title (item 1 of argv) sound name "Glass"`,
		"-e", "end run",
		title, msg)
	if err := cmd.Run(); err != nil {
		log.Printf("notify failed: %v", err)
	}
}

func trimStr(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
