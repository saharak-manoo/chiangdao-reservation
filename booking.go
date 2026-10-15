package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Defaults copied from a real browser submission (optional add-on services).
const (
	defaultService1 = `[{"id":3,"qty":1},{"id":4,"qty":0},{"id":9,"qty":0},{"id":10,"qty":0},{"id":11,"qty":0},{"id":12,"qty":0},{"id":13,"qty":0},{"id":14,"qty":0},{"id":34,"qty":0}]`
	defaultService2 = `[{"id":17,"qty":0},{"id":25,"qty":1},{"id":26,"qty":1}]`
)

type Person struct {
	Prefix, FirstName, LastName, Phone, BirthDate, IDCard, Passport string
}

// Booking is one reservation; every CSV row with the same `group` is one person in it.
// Booking-level columns are read from the group's first row.
type Booking struct {
	Group                        string
	StartDate, EndDate           string
	Email, Code                  string
	Bank, BankAccNo, BankAccName string
	Service1, Service2           string
	People                       []Person
}

func loadBookings(r io.Reader) ([]Booking, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, errors.New("csv has no data rows")
	}

	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	for _, req := range []string{"group", "start_date", "first_name", "last_name", "phone", "birth_date"} {
		if _, ok := col[req]; !ok {
			return nil, fmt.Errorf("csv missing column %q", req)
		}
	}

	var bookings []Booking
	idx := map[string]int{}
	for n, row := range rows[1:] {
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		line := n + 2
		group := get("group")
		if group == "" {
			if strings.Join(row, "") == "" {
				continue // blank line
			}
			return nil, fmt.Errorf("line %d: group is empty", line)
		}

		i, seen := idx[group]
		if !seen {
			b := Booking{
				Group:       group,
				StartDate:   get("start_date"),
				EndDate:     get("end_date"),
				Email:       get("email"),
				Code:        get("code"),
				Bank:        get("bank"),
				BankAccNo:   get("bank_acc_no"),
				BankAccName: get("bank_acc_name"),
				Service1:    or(get("service1"), defaultService1),
				Service2:    or(get("service2"), defaultService2),
			}
			if b.StartDate == "" {
				return nil, fmt.Errorf("line %d: first row of group %q needs start_date", line, group)
			}
			if b.EndDate == "" {
				return nil, fmt.Errorf("line %d: first row of group %q needs end_date", line, group)
			}
			bookings = append(bookings, b)
			i = len(bookings) - 1
			idx[group] = i
		}

		p := Person{
			Prefix:    get("prefix"),
			FirstName: get("first_name"),
			LastName:  get("last_name"),
			Phone:     get("phone"),
			BirthDate: get("birth_date"),
			IDCard:    get("id_card_number"),
			Passport:  get("passport"),
		}
		if p.FirstName == "" || p.LastName == "" || p.Phone == "" || p.BirthDate == "" {
			return nil, fmt.Errorf("line %d: first_name, last_name, phone, birth_date are required", line)
		}
		bookings[i].People = append(bookings[i].People, p)
	}
	return bookings, nil
}

// formFields builds the multipart fields in the same order as the site's JS
// (chunk 9495: reservation form builder). Pairs, because person fields repeat.
func (b Booking) formFields() ([][2]string, error) {
	f := [][2]string{
		{"start_date", b.StartDate},
		{"end_date", b.EndDate},
		{"qty_people", strconv.Itoa(len(b.People))},
	}
	for i, p := range b.People {
		bd, err := jsDateString(p.BirthDate)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", p.FirstName, p.LastName, err)
		}
		f = append(f,
			[2]string{"prefix", p.Prefix},
			[2]string{"first_name", p.FirstName},
			[2]string{"last_name", p.LastName},
			[2]string{"phone", p.Phone},
			[2]string{"birth_date", bd},
			[2]string{"id_card_number", p.IDCard},
			[2]string{"passport", p.Passport},
			[2]string{"id_card_file" + strconv.Itoa(i+1), "null"},
		)
	}
	if b.Code != "" {
		f = append(f, [2]string{"code", b.Code})
	}
	f = append(f,
		[2]string{"email", b.Email},
		[2]string{"bank", b.Bank},
		[2]string{"bank_acc_no", b.BankAccNo},
		[2]string{"bank_acc_name", b.BankAccName},
	)
	first := b.People[0]
	enc, err := buildEnc(first.FirstName, first.IDCard, first.Phone)
	if err != nil {
		return nil, err
	}
	f = append(f,
		[2]string{"enc", enc},
		[2]string{"service1", b.Service1},
		[2]string{"service2", b.Service2},
	)
	return f, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
