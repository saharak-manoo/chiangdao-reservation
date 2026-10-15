package main

import (
	"strings"
	"testing"
)

const sampleCSV = `group,start_date,end_date,prefix,first_name,last_name,phone,birth_date,id_card_number,passport,email,service1,service2
A,2026-01-31,2026-02-01,นาย,สหรัก,มณู,0931942607,1997-04-03,,,a@example.com,"[{""id"":3,""qty"":1}]","[{""id"":25,""qty"":1}]"
A,,,นาง,สมหญิง,ใจดี,0811111111,1998-05-06,1111111111111,,,,
B,2026-02-07,2026-02-08,นาย,สมชาย,ดีใจ,0822222222,1990-01-01,2222222222222,,b@example.com,,
`

func TestLoadBookingsGroupsRows(t *testing.T) {
	bs, err := loadBookings(strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 {
		t.Fatalf("want 2 bookings, got %d", len(bs))
	}
	a := bs[0]
	if a.Group != "A" || len(a.People) != 2 || a.StartDate != "2026-01-31" || a.Email != "a@example.com" {
		t.Fatalf("bad booking A: %+v", a)
	}
	if a.Service1 != `[{"id":3,"qty":1}]` {
		t.Fatalf("service1 = %q", a.Service1)
	}
	if bs[1].Service1 != defaultService1 || bs[1].Service2 != defaultService2 {
		t.Fatalf("booking B should use default services: %+v", bs[1])
	}
}

func TestLoadBookingsRejectsMissingStartDate(t *testing.T) {
	csv := "group,start_date,end_date,prefix,first_name,last_name,phone,birth_date\nA,,,นาย,x,y,081,1990-01-01\n"
	if _, err := loadBookings(strings.NewReader(csv)); err == nil {
		t.Fatal("expected error for missing start_date")
	}
}

func TestFormFieldsMatchSite(t *testing.T) {
	bs, err := loadBookings(strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := bs[0].formFields()
	if err != nil {
		t.Fatal(err)
	}
	get := func(name string) []string {
		var v []string
		for _, f := range fields {
			if f[0] == name {
				v = append(v, f[1])
			}
		}
		return v
	}
	if q := get("qty_people"); len(q) != 1 || q[0] != "2" {
		t.Fatalf("qty_people = %v", q)
	}
	if fn := get("first_name"); len(fn) != 2 || fn[1] != "สมหญิง" {
		t.Fatalf("first_name = %v", fn)
	}
	if f := get("id_card_file2"); len(f) != 1 || f[0] != "null" {
		t.Fatalf("id_card_file2 = %v", f)
	}
	if bd := get("birth_date"); bd[0] != "Thu Apr 03 1997 00:00:00 GMT+0700 (Indochina Time)" {
		t.Fatalf("birth_date = %v", bd)
	}
	enc := get("enc")
	if len(enc) != 1 {
		t.Fatalf("enc = %v", enc)
	}
	plain, err := cryptoJSDecrypt(enc[0], encPassphrase)
	if err != nil || !strings.Contains(string(plain), `"first_name":"สหรัก"`) {
		t.Fatalf("enc must describe first person, got %s (%v)", plain, err)
	}
	if len(get("code")) != 0 {
		t.Fatal("code must be omitted when empty")
	}
}
